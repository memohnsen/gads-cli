package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeOpScript records every argv line and stdin in $FAKE_OP_DIR. `item get`
// returns $FAKE_OP_DIR/item.json or a not-found error; create and edit echo
// their template back on stdout, as op echoes the item, or fail quoting it
// when $FAKE_OP_DIR/fail exists.
const fakeOpScript = `#!/bin/sh
d="$FAKE_OP_DIR"
echo "$@" >> "$d/argv"
case "$1 $2" in
"item get")
  if [ -f "$d/item.json" ]; then cat "$d/item.json"; exit 0; fi
  echo "[ERROR] 2026/11/13 10:00:00 \"$3\" isn't an item in the \"$5\" vault. Specify the item with its UUID, name, or domain." >&2
  exit 1;;
"item create"|"item edit")
  cat > "$d/stdin-$2"
  if [ -f "$d/fail" ]; then echo "[ERROR] invalid template: $(cat "$d/stdin-$2")" >&2; exit 1; fi
  cat "$d/stdin-$2"; exit 0;;
esac
exit 2
`

func fakeOp(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "op"), []byte(fakeOpScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_OP_DIR", dir)
	old := OpBinary
	OpBinary = filepath.Join(dir, "op")
	t.Cleanup(func() { OpBinary = old })
	return dir
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func templateField(t *testing.T, tmpl, field string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(tmpl), &obj); err != nil {
		t.Fatalf("template is not JSON: %v", err)
	}
	for _, f := range obj["fields"].([]any) {
		m := f.(map[string]any)
		if m["label"] == field {
			return m
		}
	}
	t.Fatalf("no field %q in the template", field)
	return nil
}

func TestWriteOpCreatesTheItemWithTheSecretOnStdin(t *testing.T) {
	dir := fakeOp(t)
	secret := userJSON(t, nil)
	ref, _ := ParseOpRef("op://Vault/Google Ads reader/credential")
	if err := WriteOp(context.Background(), ref, secret, testClientSecret, testRefreshToken); err != nil {
		t.Fatal(err)
	}
	argv := readFile(t, filepath.Join(dir, "argv"))
	assertNoSecret(t, "op argv", argv)
	if !strings.Contains(argv, "item create --vault Vault --format json -") {
		t.Fatalf("argv %q", argv)
	}
	tmpl := readFile(t, filepath.Join(dir, "stdin-create"))
	f := templateField(t, tmpl, "credential")
	if f["value"] != string(secret) || f["type"] != "CONCEALED" {
		t.Fatalf("field %v", f)
	}
	if !strings.Contains(tmpl, `"title":"Google Ads reader"`) {
		t.Fatalf("template %s", tmpl)
	}
	// The stored field value is what `op read` will hand back later.
	if c, err := ParseCredential([]byte(f["value"].(string))); err != nil || c.User.RefreshToken != testRefreshToken {
		t.Fatalf("stored value does not parse back: %v", err)
	}
}

func TestWriteOpEditsOnlyTheNamedFieldOfAnExistingItem(t *testing.T) {
	dir := fakeOp(t)
	item := `{"id":"abc123","title":"Google Ads","category":"PASSWORD","fields":[` +
		`{"id":"username","label":"username","type":"STRING","value":"keep-me"},` +
		`{"id":"x1","label":"credential","type":"CONCEALED","value":"old"}]}`
	os.WriteFile(filepath.Join(dir, "item.json"), []byte(item), 0o600)
	secret := userJSON(t, nil)
	ref, _ := ParseOpRef("op://Vault/Google Ads/credential")
	if err := WriteOp(context.Background(), ref, secret); err != nil {
		t.Fatal(err)
	}
	argv := readFile(t, filepath.Join(dir, "argv"))
	assertNoSecret(t, "op argv", argv)
	if !strings.Contains(argv, "item edit abc123 --vault Vault --format json") {
		t.Fatalf("argv %q", argv)
	}
	tmpl := readFile(t, filepath.Join(dir, "stdin-edit"))
	if templateField(t, tmpl, "credential")["value"] != string(secret) || templateField(t, tmpl, "username")["value"] != "keep-me" {
		t.Fatalf("template %s", tmpl)
	}
}

func TestWriteOpErrorsAreRedacted(t *testing.T) {
	dir := fakeOp(t)
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0o600)
	ref, _ := ParseOpRef("op://Vault/item/credential")
	err := WriteOp(context.Background(), ref, userJSON(t, nil), testClientSecret, testRefreshToken)
	if err == nil || !strings.Contains(err.Error(), "invalid template") {
		t.Fatalf("err = %v", err)
	}
	assertNoSecret(t, "op error", err.Error())
}

func TestWriteOpWithoutOp(t *testing.T) {
	old := OpBinary
	OpBinary = filepath.Join(t.TempDir(), "no-such-op")
	t.Cleanup(func() { OpBinary = old })
	ref, _ := ParseOpRef("op://Vault/item/credential")
	if err := WriteOp(context.Background(), ref, []byte("x")); err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Fatalf("err = %v", err)
	}
}

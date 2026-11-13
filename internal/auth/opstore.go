package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// OpRef is a 1Password secret reference split into its parts.
type OpRef struct{ Vault, Item, Field string }

func (r OpRef) String() string { return "op://" + r.Vault + "/" + r.Item + "/" + r.Field }

// ParseOpRef reads op://<vault>/<item>/<field>. References with a section
// (four parts) or query parameters are refused: writing needs to know which
// field to create.
func ParseOpRef(ref string) (OpRef, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(ref), "op://")
	parts := strings.Split(rest, "/")
	if !ok || len(parts) != 3 || strings.ContainsAny(rest, "?#") {
		return OpRef{}, fmt.Errorf("want a secret reference op://<vault>/<item>/<field>, got %q", ref)
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return OpRef{}, fmt.Errorf("want a secret reference op://<vault>/<item>/<field>, got %q", ref)
		}
	}
	return OpRef{Vault: parts[0], Item: parts[1], Field: parts[2]}, nil
}

// WriteOp stores secret in the field the reference names, creating the item
// (a Password item) when it does not exist and otherwise replacing only that
// field. The secret reaches op on stdin as an item JSON template, never as a
// command-line argument, and op's stdout, which echoes the item, is
// discarded. Errors quote op's first stderr line with the secret redacted.
//
// alsoRedact lists further secrets (the parts of secret) to keep out of
// error messages.
func WriteOp(ctx context.Context, ref OpRef, secret []byte, alsoRedact ...string) error {
	hide := append([]string{string(secret)}, alsoRedact...)
	if _, err := exec.LookPath(OpBinary); err != nil {
		return fmt.Errorf("the 1Password CLI (%s) is not on PATH", OpBinary)
	}
	item, err := runOp(ctx, nil, hide, "item", "get", ref.Item, "--vault", ref.Vault, "--format", "json")
	if err != nil {
		if !isOpNotFound(err) {
			return err
		}
		tmpl, err := json.Marshal(map[string]any{
			"title":    ref.Item,
			"category": "PASSWORD",
			"fields": []map[string]any{
				opField(ref.Field, string(secret)),
				{"id": "notesPlain", "type": "STRING", "purpose": "NOTES", "label": "notesPlain",
					"value": "Google Ads user OAuth credential (authorized_user JSON), written by `googleads auth login`. Revoke at https://myaccount.google.com/permissions."},
			},
		})
		if err != nil {
			return err
		}
		_, err = runOp(ctx, tmpl, hide, "item", "create", "--vault", ref.Vault, "--format", "json", "-")
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(item, &obj); err != nil {
		return errors.New("op item get returned something other than an item")
	}
	id, _ := obj["id"].(string)
	if id == "" {
		return errors.New("op item get returned an item without an id")
	}
	fields, _ := obj["fields"].([]any)
	set := false
	for _, f := range fields {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if m["label"] == ref.Field || m["id"] == ref.Field {
			m["value"], m["type"] = string(secret), "CONCEALED"
			set = true
			break
		}
	}
	if !set {
		fields = append(fields, opField(ref.Field, string(secret)))
	}
	obj["fields"] = fields
	tmpl, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = runOp(ctx, tmpl, hide, "item", "edit", id, "--vault", ref.Vault, "--format", "json")
	return err
}

func opField(name, value string) map[string]any {
	f := map[string]any{"id": name, "label": name, "type": "CONCEALED", "value": value}
	if name == "password" {
		f["purpose"] = "PASSWORD"
	}
	return f
}

type opError struct{ msg string }

func (e *opError) Error() string { return e.msg }

func isOpNotFound(err error) bool {
	var oe *opError
	if !errors.As(err, &oe) {
		return false
	}
	m := strings.ToLower(oe.msg)
	return strings.Contains(m, "isn't an item") || strings.Contains(m, "not found") || strings.Contains(m, "no item found")
}

// runOp runs op with stdin (nil: closed) and a timeout, returns its stdout,
// and turns a failure into an *opError quoting op's first stderr line with
// the secrets redacted.
func runOp(ctx context.Context, stdin []byte, secrets []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, OpBinary, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, &opError{fmt.Sprintf("op %s timed out after %s (is 1Password unlocked, or OP_SERVICE_ACCOUNT_TOKEN set?)", args[1], opTimeout)}
		}
		msg := strings.TrimSpace(firstLine(Redact(stderr.String(), secrets...)))
		if msg == "" {
			msg = err.Error()
		}
		return nil, &opError{fmt.Sprintf("op item %s failed: %s", args[1], msg)}
	}
	return stdout.Bytes(), nil
}

// Redact replaces every occurrence of each non-empty secret in s, and of the
// secret's JSON-escaped form, with [redacted].
func Redact(s string, secrets ...string) string {
	for _, sec := range secrets {
		if len(sec) < 4 {
			continue
		}
		s = strings.ReplaceAll(s, sec, "[redacted]")
		if q, err := json.Marshal(sec); err == nil {
			s = strings.ReplaceAll(s, string(q[1:len(q)-1]), "[redacted]")
		}
	}
	return s
}

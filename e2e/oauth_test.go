package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUserOAuthSmoke drives the binary through user OAuth: a headless
// `auth login` for both roles answered over the loopback, then status, a
// read, a dry run, the refused and the confirmed --apply. No output, and no file but the two
// stored credentials, may hold the client secret, the refresh token or the
// authorization code; every safety gate behaves as with a service account.
func TestUserOAuthSmoke(t *testing.T) {
	bin := buildBinary(t)
	g := newFakeGoogle(t)
	env := scrubbedEnv(t,
		"GOOGLE_ADS_TOKEN_URL="+g.URL+"/token", "GOOGLE_ADS_API_BASE="+g.URL, "GOOGLE_ADS_ALLOW_CUSTOM_BASE=1",
		"GOOGLE_ADS_CUSTOMER_ID=123-456-7890", "DISPLAY=", "WAYLAND_DISPLAY=")
	var home string
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			home = strings.TrimPrefix(kv, "HOME=")
		}
	}
	client := filepath.Join(t.TempDir(), "client.json")
	raw := `{"installed":{"client_id":"` + e2eClientID + `","client_secret":"` + e2eClientSecret + `"}}`
	if err := os.WriteFile(client, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var outputs []string
	check := func(where, s string) {
		t.Helper()
		outputs = append(outputs, s)
		if strings.Contains(s, "SENTINEL") {
			t.Fatalf("%s leaks a secret: %s", where, s)
		}
	}

	login := func(role string) {
		t.Helper()
		cmd := exec.Command(bin, "auth", "login", "--role", role, "--no-browser", "--client-file", client)
		cmd.Env = env
		stdout, _ := cmd.StdoutPipe()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		stdin, _ := cmd.StdinPipe()
		defer stdin.Close()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		rd := bufio.NewReader(stdout)
		first, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("no consent URL: %v %s", err, stderr.String())
		}
		u, _ := url.Parse(strings.TrimSpace(first))
		q := u.Query()
		if u.Host != "accounts.google.com" || q.Get("scope") != "https://www.googleapis.com/auth/adwords" {
			t.Fatalf("consent URL %s", first)
		}
		resp, err := http.Get(q.Get("redirect_uri") + "?code=" + e2eCode + "&state=" + url.QueryEscape(q.Get("state")))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		rest, _ := readAll(rd)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("login %s: %v %s", role, err, stderr.String())
		}
		check("login stdout", first+rest)
		check("login stderr", stderr.String())
		if !strings.Contains(rest, role+" credential (user OAuth") {
			t.Fatalf("stdout %q", rest)
		}
	}
	login("reader")
	login("writer")

	out, errOut, code := run(t, bin, env, "", "auth", "status")
	check("status", out+errOut)
	if code != 0 || !strings.Contains(out, `"mode":"authorized_user"`) {
		t.Fatalf("exit %d %s %s", code, out, errOut)
	}

	out, errOut, code = run(t, bin, env, "", "search", "--query", "SELECT campaign.id FROM campaign")
	check("search", out+errOut)
	if code != 0 || !strings.Contains(out, `"results"`) || g.devToken.Load() {
		t.Fatalf("exit %d %s %s", code, out, errOut)
	}

	before := g.mutates.Load()
	paused := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x","status":"PAUSED"}}}]}`
	out, errOut, code = run(t, bin, env, "", "mutate", "--data", paused)
	check("dry run", out+errOut)
	if code != 0 || g.mutates.Load() != before+1 || !strings.Contains(g.body(), `"validateOnly":true`) {
		t.Fatalf("a write without --apply must be a validate_only dry run: exit %d %s %s body %s", code, out, errOut, g.body())
	}

	noStatus := `{"mutateOperations":[{"campaignOperation":{"create":{"name":"x"}}}]}`
	out, errOut, code = run(t, bin, env, "", "mutate", "--validate-only", "--data", noStatus)
	check("create without PAUSED", out+errOut)
	if code != 2 || !strings.Contains(errOut, "PAUSED") {
		t.Fatalf("a create without PAUSED must be refused: exit %d %s", code, errOut)
	}

	enable := `{"mutateOperations":[{"campaignOperation":{"update":{"resourceName":"customers/1234567890/campaigns/2","status":"ENABLED"},"updateMask":"status"}}]}`
	before = g.mutates.Load()
	for _, flags := range [][]string{{"--apply"}, {"--apply", "--confirm-spend"}} {
		out, errOut, code = run(t, bin, env, "", append([]string{"mutate", "--data", enable}, flags...)...)
		check("apply in read-only mode", out+errOut)
		if code != 2 || g.mutates.Load() != before {
			t.Fatalf("%v: read-only mode (the default) must refuse every --apply: exit %d %s", flags, code, errOut)
		}
	}
	if _, errOut, code := run(t, bin, env, "", "config", "set", "read-only", "false"); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	out, errOut, code = run(t, bin, env, "", "mutate", "--apply", "--data", enable)
	check("enable without --confirm-spend", out+errOut)
	if code != 2 || !strings.Contains(errOut, "confirm-spend") || g.mutates.Load() != before {
		t.Fatalf("enabling needs --apply --confirm-spend: exit %d %s", code, errOut)
	}
	out, errOut, code = run(t, bin, env, "", "mutate", "--apply", "--confirm-spend", "--data", enable)
	check("confirmed apply", out+errOut)
	if code != 0 || g.mutates.Load() != before+1 || strings.Contains(g.body(), "validateOnly") {
		t.Fatalf("confirmed apply with the writer user credential: exit %d %s", code, errOut)
	}

	audit, err := os.ReadFile(filepath.Join(home, "state", "gads", "mutations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var sawDryRun, sawApply bool
	for _, line := range strings.Split(strings.TrimSpace(string(audit)), "\n") {
		var e map[string]any
		json.Unmarshal([]byte(line), &e)
		if e["result"] == "dry_run" && e["credential_type"] == "authorized_user" && e["credential"] == "reader" {
			sawDryRun = true
		}
		if e["result"] == "applied" && e["credential_type"] == "authorized_user" && e["credential"] == "writer" && e["confirm_spend"] == true {
			sawApply = true
		}
	}
	if !sawDryRun || !sawApply {
		t.Fatalf("audit log lacks the user-OAuth dry run or apply:\n%s", audit)
	}

	// Only the two stored credentials may hold the secrets: not the config
	// file, the token cache or the audit log.
	// The config dir is OS-specific (~/Library/Application Support on macOS
	// ignores XDG_CONFIG_HOME), so find gads/credentials wherever it is.
	isCredDir := func(dir string) bool {
		return filepath.Base(dir) == "credentials" && filepath.Base(filepath.Dir(dir)) == "gads"
	}
	stored := map[string]bool{}
	filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if isCredDir(filepath.Dir(p)) {
			if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v %v", p, err, fi)
			}
			stored[filepath.Base(p)] = true
			return nil
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "SENTINEL") {
			t.Errorf("%s holds a secret", p)
		}
		return nil
	})
	if !stored["reader.json"] || !stored["writer.json"] {
		t.Fatalf("stored credentials %v, want reader.json and writer.json", stored)
	}
}

func readAll(r *bufio.Reader) (string, error) {
	b, err := io.ReadAll(r)
	return string(b), err
}

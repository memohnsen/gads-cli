package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

type fakePrompter struct{ answers []string }

func (f *fakePrompter) Line(string) (string, error) {
	if len(f.answers) == 0 {
		return "", io.EOF
	}
	s := f.answers[0]
	f.answers = f.answers[1:]
	return s, nil
}
func (f *fakePrompter) Secret(p string) (string, error)    { return f.Line(p) }
func (f *fakePrompter) Confirm(string, bool) (bool, error) { return true, nil }

// initApp wires an app to a fake Google (token endpoint and API on one
// server) with a terminal and scripted answers.
func initApp(t *testing.T, searchStatus int, answers ...string) (*app, string) {
	t.Helper()
	home := isolate(t)
	srv := fakeGoogle(t, searchStatus)
	a, _, _ := testApp(t, &fakeServer{Server: srv}, config.Resolved{TokenURL: srv.URL + "/token", AllowCustomBase: true})
	a.isTerminal = func() bool { return true }
	a.prompt = &fakePrompter{answers: answers}
	return a, home
}

func TestInitRefusesWithoutTerminal(t *testing.T) {
	isolate(t)
	a, _, errb := testApp(t, nil, config.Resolved{})
	a.isTerminal = func() bool { return false }
	if code := a.run([]string{"init"}); code != 2 || !strings.Contains(errb.String(), "terminal") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

func TestInitExpandsHomeInKeyPath(t *testing.T) {
	// The first cap answer uses a decimal comma and must be asked again.
	a, home := initApp(t, 200, "~/key.json", "123-456-7890", "29,61", "29.61")
	if err := os.WriteFile(filepath.Join(home, "key.json"), testKeyJSON(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := a.run([]string{"init"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	c, err := config.Load()
	if err != nil || len(c.ServiceAccountKey) == 0 || c.CustomerID != "1234567890" || c.BudgetCapMicros != 29610000 {
		t.Fatalf("saved %+v %v", c, err)
	}
	out := a.stdout.(interface{ String() string }).String()
	if !strings.Contains(out, "Muster AG") || !strings.Contains(out, "CHF") || !strings.Contains(out, "delete the downloaded copy") {
		t.Fatalf("stdout %q", out)
	}
}

func TestInitSavesNothingWhenVerificationFails(t *testing.T) {
	a, home := initApp(t, 403, "~/key.json", "123-456-7890", "29.61")
	os.WriteFile(filepath.Join(home, "key.json"), testKeyJSON(t), 0o600)
	if code := a.run([]string{"init"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if c, _ := config.Load(); c.CustomerID != "" || len(c.ServiceAccountKey) != 0 {
		t.Fatalf("a failed verification saved %+v", c)
	}
	if errOut := a.stderr.(interface{ String() string }).String(); !strings.Contains(errOut, "Access and security") {
		t.Fatalf("stderr %q", errOut)
	}
}

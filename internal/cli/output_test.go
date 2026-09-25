package cli

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/config"
)

// A path that cannot be written is caught before the request, so usage
// keeps meaning "nothing was sent".
func TestOutputBadPathNothingSent(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	bad := filepath.Join(t.TempDir(), "missing", "out.json")
	for _, args := range [][]string{
		{"campaigns", "mutate", "--customer-id", "1234567890", "--data", pausedCampaign, "--output", bad},
		{"search", "--customer-id", "1234567890", "--query", "SELECT campaign.id FROM campaign", "--all", "--output", bad},
		{"api", "GET", "v25/customers:listAccessibleCustomers", "--output", bad},
		{"api", "POST", "v25/customers/1234567890/campaigns:mutate", "--data", pausedCampaign, "--output", t.TempDir()},
	} {
		a, _, errb := testApp(t, srv, config.Resolved{})
		if code := a.run(args); code != 2 || errJSON(t, errb)["kind"] != "usage" {
			t.Errorf("%v: exit %d %s", args, code, errb.String())
		}
	}
	if n := srv.hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

func TestOutputWritesTheFile(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[{"resourceName":"customers/1234567890/campaigns/1"}]}`))
	p := writeFile(t, "out.json", []byte("an older, longer response that must not survive"))
	a, out, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"campaigns", "mutate", "--customer-id", "1234567890", "--data", pausedCampaign, "--output", p}); code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	got, _ := os.ReadFile(p)
	if string(got) != `{"results":[{"resourceName":"customers/1234567890/campaigns/1"}]}` || out.Len() != 0 {
		t.Fatalf("file %q stdout %q", got, out.String())
	}
}

// A failed request leaves an existing file as it was and removes a file the
// call created.
func TestOutputFailedRequestLeavesFilesAlone(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":400,"message":"bad","status":"INVALID_ARGUMENT"}}`))
	})
	existing := writeFile(t, "old.json", []byte("old"))
	fresh := filepath.Join(t.TempDir(), "new.json")
	for _, p := range []string{existing, fresh} {
		a, _, errb := testApp(t, srv, config.Resolved{})
		if code := a.run([]string{"campaigns", "mutate", "--customer-id", "1234567890", "--data", pausedCampaign, "--output", p}); code != 1 {
			t.Fatalf("exit %d %s", code, errb.String())
		}
	}
	if got, _ := os.ReadFile(existing); string(got) != "old" {
		t.Fatalf("existing file changed to %q", got)
	}
	if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file the failed call created is still there: %v", err)
	}
}

// After a 2xx the change has happened: a failed file write must not read as
// usage (an agent would repeat the call), and the response must not be lost.
func TestOutputWriteFailureAfterSuccess(t *testing.T) {
	a, out, errb := testApp(t, nil, config.Resolved{})
	o, err := openOutput(filepath.Join(t.TempDir(), "out.json"))
	if err != nil {
		t.Fatal(err)
	}
	o.f.Close() // the write that follows fails
	body := `{"results":[{"resourceName":"customers/1234567890/campaigns/1"}]}`
	err = a.writeResponse(&api.Response{Status: 200, Body: []byte(body)}, o)
	if code := a.renderError(err); code != 1 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	e := errJSON(t, errb)
	if e["kind"] == "usage" || !strings.Contains(e["error"].(string), "succeeded") {
		t.Fatalf("%v", e)
	}
	if out.String() != body+"\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestStdoutWriteFailureIsReported(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	a, _, errb := testApp(t, srv, config.Resolved{})
	a.stdout = failingWriter{}
	code := a.run([]string{"campaigns", "mutate", "--customer-id", "1234567890", "--data", pausedCampaign})
	if e := errJSON(t, errb); code != 1 || e["kind"] == "usage" || !strings.Contains(e["error"].(string), "succeeded") {
		t.Fatalf("exit %d %s", code, errb.String())
	}
}

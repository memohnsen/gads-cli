package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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

func TestMaxPagesBelowOneIsUsage(t *testing.T) {
	srv := newFakeServer(t, reply(`{"results":[]}`))
	for _, n := range []string{"0", "-1"} {
		a, out, errb := testApp(t, srv, config.Resolved{})
		code := a.run([]string{"search", "--customer-id", "1234567890", "--query", "SELECT campaign.id FROM campaign", "--all", "--max-pages", n})
		if code != 2 || !strings.Contains(errJSON(t, errb)["error"].(string), "--max-pages") || out.Len() != 0 {
			t.Errorf("--max-pages %s: exit %d stdout %q %s", n, code, out.String(), errb.String())
		}
	}
	if n := srv.hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

// A device such as /dev/null cannot be truncated; writing to it must still work.
func TestOutputToNonRegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/null on Windows")
	}
	srv := newFakeServer(t, reply(`{"resourceNames":[]}`))
	a, out, errb := testApp(t, srv, config.Resolved{})
	if code := a.run([]string{"api", "GET", "v25/customers:listAccessibleCustomers", "--output", os.DevNull}); code != 0 || out.Len() != 0 {
		t.Fatalf("exit %d stdout %q %s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(os.DevNull); err != nil {
		t.Fatalf("%s is gone: %v", os.DevNull, err)
	}
	// A pipe, as with --output /dev/stdout under a shell pipeline: macOS
	// lets /dev/null be truncated, a pipe refuses on every Unix.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got := make(chan string)
	go func() { b, _ := io.ReadAll(r); got <- string(b) }()
	a, out, errb = testApp(t, srv, config.Resolved{})
	code := a.run([]string{"api", "GET", "v25/customers:listAccessibleCustomers", "--output", fmt.Sprintf("/dev/fd/%d", w.Fd())})
	w.Close()
	if body := <-got; code != 0 || body != `{"resourceNames":[]}` || out.Len() != 0 {
		t.Fatalf("pipe: exit %d, pipe got %q, stdout %q %s", code, body, out.String(), errb.String())
	}
}

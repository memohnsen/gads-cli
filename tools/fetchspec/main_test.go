package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(kind, version string, methods int) []byte {
	ms := map[string]any{}
	for i := 0; i < methods; i++ {
		ms["m"+string(rune('a'+i))] = map[string]any{"id": "googleads.x"}
	}
	doc := map[string]any{
		"kind": kind, "version": version, "revision": "20260923",
		"resources": map[string]any{"customers": map[string]any{
			"methods":   ms,
			"resources": map[string]any{"campaigns": map[string]any{"methods": map[string]any{"mutate": map[string]any{}}}},
		}},
		"schemas": map[string]any{"A": map[string]any{}, "B": map[string]any{}},
	}
	raw, _ := json.Marshal(doc)
	return raw
}

func TestValidateAccepts(t *testing.T) {
	p, err := validate(fixture("discovery#restDescription", "v25", 3), "v25", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p.Methods != 4 || p.Schemas != 2 || p.Revision != "20260923" || len(p.SHA256) != 64 {
		t.Fatalf("provenance %+v", p)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]struct {
		raw     []byte
		wantErr string
	}{
		"not json":      {[]byte("<html>"), "not JSON"},
		"wrong kind":    {fixture("something", "v25", 3), "kind"},
		"wrong version": {fixture("discovery#restDescription", "v24", 3), "version"},
		"too few":       {fixture("discovery#restDescription", "v25", 1), "methods"},
	}
	for name, c := range cases {
		_, err := validate(c.raw, "v25", 4, 2)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, c.wantErr)
		}
	}
}

func TestRunWritesSpecAndProvenance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/$discovery/rest" || r.URL.Query().Get("version") != "v25" {
			http.NotFound(w, r)
			return
		}
		w.Write(fixture("discovery#restDescription", "v25", 3))
	}))
	defer srv.Close()
	dir := t.TempDir()
	if _, err := run(srv.Client(), srv.URL, "v25", dir, time.Unix(0, 0), 4, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "googleads-v25.discovery.json")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "PROVENANCE.json"))
	if err != nil || !strings.Contains(string(raw), `"revision": "20260923"`) {
		t.Fatalf("provenance: %s %v", raw, err)
	}
}

func TestRunWritesNothingOnBadDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture("discovery#restDescription", "v24", 3))
	}))
	defer srv.Close()
	dir := t.TempDir()
	if _, err := run(srv.Client(), srv.URL, "v25", dir, time.Now(), 4, 2); err == nil {
		t.Fatal("want error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("wrote %d files", len(entries))
	}
}

func TestRunHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := run(srv.Client(), srv.URL, "v26", t.TempDir(), time.Now(), 4, 2); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

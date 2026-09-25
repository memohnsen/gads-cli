package cli

import (
	"runtime/debug"
	"testing"
)

func TestVersionFallsBackToModuleVersion(t *testing.T) {
	info := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	cases := []struct {
		linker string
		info   *debug.BuildInfo
		ok     bool
		want   string
	}{
		{"v0.2.0", info("v0.1.0"), true, "v0.2.0"}, // the linker-set version wins
		{"dev", info("v0.1.0"), true, "v0.1.0"},    // go install ...@v0.1.0
		{"dev", info("(devel)"), true, "dev"},      // go build in a checkout
		{"dev", info(""), true, "dev"},
		{"dev", nil, false, "dev"}, // no build info
	}
	for _, tc := range cases {
		if got := resolveVersion(tc.linker, tc.info, tc.ok); got != tc.want {
			t.Errorf("resolveVersion(%q, %v, %v) = %q, want %q", tc.linker, tc.info, tc.ok, got, tc.want)
		}
	}
}

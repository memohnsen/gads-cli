package cli

import (
	"os"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
)

// writeResponse puts the body on stdout, or in outputPath, as Google sent it.
// The one change is a trailing newline when the body lacks one.
func (a *app) writeResponse(resp *api.Response, outputPath string) error {
	if len(resp.Body) == 0 {
		return nil
	}
	if outputPath != "" {
		if err := os.WriteFile(outputPath, resp.Body, 0o644); err != nil {
			return api.Usagef("writing %s: %v", outputPath, err)
		}
		return nil
	}
	a.stdout.Write(resp.Body)
	if resp.Body[len(resp.Body)-1] != '\n' {
		a.stdout.Write([]byte("\n"))
	}
	return nil
}

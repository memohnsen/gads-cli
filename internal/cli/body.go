package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/memohnsen/gads-cli/internal/api"
)

// maxJSONBody caps --data so a mistaken `--data @/dev/urandom` fails locally.
const maxJSONBody = 20 << 20

// readJSONBody resolves --data: "-" reads stdin, "@path" reads a file,
// anything else is the literal body. (nil, nil) when --data was not given.
func (a *app) readJSONBody(cmd *cobra.Command) ([]byte, error) {
	val := flagString(cmd, "data")
	if val == "" {
		return nil, nil
	}
	var raw []byte
	var err error
	switch {
	case val == "-":
		raw, err = io.ReadAll(io.LimitReader(a.stdin, maxJSONBody+1))
	case strings.HasPrefix(val, "@"):
		raw, err = os.ReadFile(val[1:])
	default:
		raw = []byte(val)
	}
	if err != nil {
		return nil, api.Usagef("reading --data: %v", err)
	}
	if len(raw) > maxJSONBody {
		return nil, api.Usagef("--data exceeds the 20 MB limit")
	}
	if raw, err = decodeText(raw, "--data"); err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, api.Usagef("--data is not valid JSON")
	}
	return raw, nil
}

// decodeText strips a UTF-8 byte order mark and refuses UTF-16, which
// PowerShell 5 writes by default, with a message that names the fix.
func decodeText(raw []byte, what string) ([]byte, error) {
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return raw[3:], nil
	}
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) || bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) ||
		(len(raw) >= 2 && (raw[0] == 0 || raw[1] == 0)) {
		return nil, api.Usagef("%s is UTF-16 encoded; save it as UTF-8 (in PowerShell: Set-Content -Encoding utf8)", what)
	}
	return raw, nil
}

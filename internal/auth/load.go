package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/memohnsen/gads-cli/internal/api"
	"github.com/memohnsen/gads-cli/internal/config"
)

// OpBinary is the 1Password CLI; tests point it at a fake.
var OpBinary = "op"

// opTimeout bounds `op read`, which may wait on a desktop-app unlock.
const opTimeout = 60 * time.Second

// maxKeyBytes bounds a credential read from any source.
const maxKeyBytes = 1 << 20

// LoadCredential reads and validates one role's credential: a
// service-account key or an authorized_user credential. The secret never
// passes through a command line or an error message: op writes it to a pipe
// this process reads, and a file is read directly after its permissions are
// checked. Every error is a usage *api.Error.
func LoadCredential(ctx context.Context, c config.Credential) (*Credential, error) {
	raw, err := readSource(ctx, c)
	if err != nil {
		return nil, err
	}
	cred, err := ParseCredential(raw)
	if err != nil {
		return nil, api.Usagef("%s credential (%s): %v", c.Role, describeSource(c), err)
	}
	return cred, nil
}

// LoadKey reads and validates a role's credential and requires it to be a
// service-account key.
func LoadKey(ctx context.Context, c config.Credential) (*Key, error) {
	raw, err := readSource(ctx, c)
	if err != nil {
		return nil, err
	}
	k, err := ParseKey(raw)
	if err != nil {
		return nil, api.Usagef("%s key (%s): %v", c.Role, describeSource(c), err)
	}
	return k, nil
}

func readSource(ctx context.Context, c config.Credential) ([]byte, error) {
	var raw []byte
	var err error
	switch c.Source {
	case config.SourceOp:
		raw, err = readOp(ctx, c.OpRef)
	case config.SourceEnv, config.SourceFile:
		raw, err = readPrivateFile(c.Path)
	default:
		return nil, api.Usagef("no %s credential is configured; %s", c.Role, SetupHint(c.Role))
	}
	if err != nil {
		return nil, api.Usagef("%s credential (%s): %v", c.Role, describeSource(c), err)
	}
	return raw, nil
}

// LoadClient reads an OAuth client file for `auth login`, from a 1Password
// reference or from a file that, like a credential, must be chmod 600: its
// client secret is half of what a refresh token needs.
func LoadClient(ctx context.Context, path, opRef string) (*OAuthClient, error) {
	var raw []byte
	var err error
	where := path
	switch {
	case opRef != "":
		where = "1Password " + opRef
		if !strings.HasPrefix(opRef, "op://") {
			return nil, api.Usagef("--client-op-ref wants a secret reference such as op://<vault>/<item>/<field>, got %q", opRef)
		}
		raw, err = readOp(ctx, opRef)
	case path != "":
		raw, err = readPrivateFile(path)
	default:
		return nil, api.Usagef("auth login needs the OAuth client: --client-file <client.json> (chmod 600) or --client-op-ref op://<vault>/<item>/<field>")
	}
	if err != nil {
		return nil, api.Usagef("OAuth client (%s): %v", where, err)
	}
	cl, err := ParseClientFile(raw)
	if err != nil {
		return nil, api.Usagef("OAuth client (%s): %v", where, err)
	}
	return cl, nil
}

// SetupHint names the ways to configure a role's credential.
func SetupHint(role string) string {
	if role == config.RoleWriter {
		return "run `googleads auth login --role writer --client-file <client.json>` (user OAuth), " +
			"set op_writer_ref with `googleads config set op-ref --role writer op://<vault>/<item>/<field>`, " +
			"point GOOGLE_ADS_WRITE_JSON_KEY_FILE_PATH at a chmod 600 credential file, " +
			"or store a service-account key with `googleads config set service-account-key --role writer < key.json`"
	}
	return "run `googleads auth login --client-file <client.json>` (user OAuth), " +
		"set op_reader_ref with `googleads config set op-ref op://<vault>/<item>/<field>`, " +
		"point GOOGLE_ADS_JSON_KEY_FILE_PATH at a chmod 600 credential file, " +
		"or store a service-account key with `googleads config set service-account-key < key.json`"
}

func describeSource(c config.Credential) string {
	switch c.Source {
	case config.SourceOp:
		return "1Password " + c.OpRef
	case config.SourceEnv:
		return c.EnvName + "=" + c.Path
	}
	return c.Path
}

func readPrivateFile(path string) ([]byte, error) {
	if err := config.CheckPrivate(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxKeyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxKeyBytes {
		return nil, errors.New("file is larger than 1 MB; not a credential")
	}
	return raw, nil
}

// readOp runs `op read --no-newline <ref>` with stdin closed, so it can never
// stop to prompt, and a timeout. Its stderr may be quoted in an error; its
// stdout, the key, never is.
func readOp(ctx context.Context, ref string) ([]byte, error) {
	if _, err := exec.LookPath(OpBinary); err != nil {
		return nil, fmt.Errorf("the 1Password CLI (%s) is not on PATH", OpBinary)
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, OpBinary, "read", "--no-newline", ref)
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("op read timed out after %s (is 1Password unlocked, or OP_SERVICE_ACCOUNT_TOKEN set?)", opTimeout)
		}
		msg := strings.TrimSpace(firstLine(stderr.String()))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("op read failed: %s", msg)
	}
	if stdout.Len() > maxKeyBytes {
		return nil, errors.New("op returned more than 1 MB; not a credential")
	}
	return stdout.Bytes(), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

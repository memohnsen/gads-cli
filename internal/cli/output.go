package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
)

// outputFile is the file --output names. It is opened before the request,
// so a path that cannot be written fails as usage with nothing sent. It is
// not truncated until the response is in hand, so a failed request leaves an
// existing file as it was.
type outputFile struct {
	path    string
	f       *os.File
	created bool // this call created the file, so discard removes it
}

// openOutput opens path for writing; "" means stdout and returns nil.
func openOutput(path string) (*outputFile, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	created := err == nil
	if errors.Is(err, fs.ErrExist) {
		f, err = os.OpenFile(path, os.O_WRONLY, 0)
	}
	if err != nil {
		return nil, api.Usagef("--output %s cannot be written: %v; nothing was sent", path, err)
	}
	return &outputFile{path: path, f: f, created: created}, nil
}

// write replaces the file's content with body and closes it.
func (o *outputFile) write(body []byte) error {
	f := o.f
	o.f = nil
	err := f.Truncate(0)
	if err == nil {
		_, err = f.Write(body)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// discard closes a file no response was written to, and removes it when
// this call created it. It is a no-op after write, and on nil.
func (o *outputFile) discard() {
	if o == nil || o.f == nil {
		return
	}
	o.f.Close()
	o.f = nil
	if o.created {
		os.Remove(o.path)
	}
}

// writeResponse puts the body in out, or on stdout when out is nil, as
// Google sent it. The one change is a trailing newline on stdout when the
// body lacks one.
//
// It runs after a 2xx, so whatever the call changed has changed. A failed
// write is therefore never a usage error, which would tell an agent that
// nothing was sent: it is output_failed, exit 1, and a response the file
// could not take goes to stdout instead.
func (a *app) writeResponse(resp *api.Response, out *outputFile) error {
	if out != nil {
		err := out.write(resp.Body)
		if err == nil {
			return nil
		}
		if serr := a.writeStdout(resp.Body); serr != nil {
			return &api.Error{Kind: api.KindOutputFailed, Status: resp.Status, Message: fmt.Sprintf(
				"the call succeeded, but writing --output %s failed (%v) and writing the response to stdout failed too (%v); the response is lost",
				out.path, err, serr)}
		}
		return &api.Error{Kind: api.KindOutputFailed, Status: resp.Status, Message: fmt.Sprintf(
			"the call succeeded, but writing --output %s failed: %v; the response is on stdout instead", out.path, err)}
	}
	if err := a.writeStdout(resp.Body); err != nil {
		return &api.Error{Kind: api.KindOutputFailed, Status: resp.Status, Message: fmt.Sprintf(
			"the call succeeded, but writing the response to stdout failed: %v", err)}
	}
	return nil
}

func (a *app) writeStdout(body []byte) error {
	if len(body) == 0 {
		return nil
	}
	if _, err := a.stdout.Write(body); err != nil {
		return err
	}
	if body[len(body)-1] != '\n' {
		if _, err := a.stdout.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return nil
}

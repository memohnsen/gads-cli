package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// prompter reads the wizard's answers. Terminal access lives behind this
// interface for one reason: the whole of `init` is a sequence of questions,
// and a wizard that can only be exercised by a human at a keyboard is a
// wizard with no tests.
type prompter interface {
	Line(prompt string) (string, error)   // echoed
	Secret(prompt string) (string, error) // not echoed
	Confirm(prompt string, def bool) (bool, error)
}

// termPrompter is the production prompter.
type termPrompter struct {
	in  *os.File
	out io.Writer
	r   *bufio.Reader
}

func newTermPrompter(in *os.File, out io.Writer) *termPrompter {
	return &termPrompter{in: in, out: out, r: bufio.NewReader(in)}
}

func (p *termPrompter) Line(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	s, err := p.r.ReadString('\n')
	// A read that returns nothing at all is a closed stdin, not an empty
	// answer: the caller must abort rather than re-prompt forever.
	if err != nil && s == "" {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func (p *termPrompter) Secret(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	b, err := term.ReadPassword(int(p.in.Fd()))
	// ReadPassword consumes the user's Enter without echoing it, so the
	// cursor is still on the prompt line; without this the next line of
	// narration would be appended to it.
	fmt.Fprintln(p.out)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Confirm re-asks on anything it does not recognise, bounded so a stream of
// junk cannot loop forever. Bare Enter takes the default.
func (p *termPrompter) Confirm(prompt string, def bool) (bool, error) {
	suffix := " [y/N] "
	if def {
		suffix = " [Y/n] "
	}
	for range 3 {
		s, err := p.Line(prompt + suffix)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.out, "Please answer y or n.")
	}
	return def, nil
}

// ask insists on a non-empty answer, bounded so a stream of blank lines
// cannot loop forever. A wizard that aborted on one stray Enter would make
// the user redo every step that came before it.
func ask(p prompter, prompt string, secret bool) (string, error) {
	for range 3 {
		var (
			s   string
			err error
		)
		if secret {
			s, err = p.Secret(prompt)
		} else {
			s, err = p.Line(prompt)
		}
		if err != nil {
			return "", err // closed stdin: nobody left to ask
		}
		if s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("no value entered after 3 attempts")
}

// stdioIsTerminal reports whether there is a human on both ends: stdin to
// answer the questions, stdout to read them. A pipe, a file or a CI runner
// means nobody is there, and a wizard that prompts into the void would hang
// rather than fail.
func stdioIsTerminal() bool { return isTerminal(os.Stdin, os.Stdout) }

// isTerminal asks the terminal driver, rather than testing os.ModeCharDevice
// on a Stat().
//
// A character device is not the same thing as a terminal: /dev/null and
// /dev/zero are both character devices, so the mode test let `googleads init <
// /dev/null` through the gate — narration onto stdout, then a failure on the
// first prompt nobody could answer — and `< /dev/zero` through to
// bufio.ReadString growing a buffer without bound. term.IsTerminal is the exact
// test, and the correct one on Windows, where a console handle is not a
// character device in the Unix sense.
//
// A pty still passes, because a pty is a terminal: a harness that allocates one
// gets the wizard, not a refusal. There is no predicate that distinguishes a
// human at a pty from a program driving one, which is why docs/agents.md tells
// agents not to run init rather than promising the gate will stop them.
func isTerminal(files ...*os.File) bool {
	for _, f := range files {
		if !term.IsTerminal(int(f.Fd())) {
			return false
		}
	}
	return true
}

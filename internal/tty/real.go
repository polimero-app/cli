package tty

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// Real implements Prompter using the real terminal.
type Real struct{}

// NewReal returns a Real Prompter.
func NewReal() *Real { return &Real{} }

func (r *Real) IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// ReadHidden reads a secret from the terminal with echo disabled.
// term.ReadPassword puts the terminal in no-echo mode; if the process is
// interrupted mid-read the terminal would stay in that state, so a signal
// handler restores the saved state before re-raising the signal.
func (r *Real) ReadHidden(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	_, _ = fmt.Fprint(os.Stderr, prompt)

	saved, stateErr := term.GetState(fd)
	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})
	if stateErr == nil {
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			select {
			case sig := <-sigCh:
				_ = term.Restore(fd, saved)
				_, _ = fmt.Fprintln(os.Stderr)
				signal.Stop(sigCh)
				reraise(sig)
			case <-done:
			}
		}()
	}

	b, err := term.ReadPassword(fd)
	if stateErr == nil {
		close(done)
		signal.Stop(sigCh)
	}
	_, _ = fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *Real) ReadLine(prompt string) (string, error) {
	_, _ = fmt.Fprint(os.Stderr, prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}

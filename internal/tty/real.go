package tty

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// Real implements Prompter using the real terminal.
type Real struct{}

// NewReal returns a Real Prompter.
func NewReal() *Real { return &Real{} }

func (r *Real) IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func (r *Real) ReadHidden(prompt string) (string, error) {
	_, _ = fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
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
		return strings.TrimRight(scanner.Text(), "\r\n"), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

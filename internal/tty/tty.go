package tty

// Prompter abstracts terminal interaction for commands that need user input.
type Prompter interface {
	// IsTerminal reports whether stdin is an interactive terminal.
	IsTerminal() bool
	// ReadHidden prints prompt to stderr and reads a line with echo disabled.
	ReadHidden(prompt string) (string, error)
	// ReadLine prints prompt to stderr and reads a line with echo enabled.
	ReadLine(prompt string) (string, error)
}

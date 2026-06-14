package tty

// Mock is a Prompter implementation for use in tests.
// Set Terminal=true to simulate an interactive session.
// Set HiddenVal for ReadHidden responses; populate Lines for sequential ReadLine responses.
// Set Err to inject errors on all read calls.
type Mock struct {
	Terminal  bool
	HiddenVal string
	Lines     []string
	Err       error
	lineIdx   int
}

func (m *Mock) IsTerminal() bool { return m.Terminal }

func (m *Mock) ReadHidden(_ string) (string, error) {
	return m.HiddenVal, m.Err
}

func (m *Mock) ReadLine(_ string) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	if m.lineIdx >= len(m.Lines) {
		return "", nil
	}
	v := m.Lines[m.lineIdx]
	m.lineIdx++
	return v, nil
}

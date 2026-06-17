package tty

import (
	"io"
	"testing"
)

func TestMock_IsTerminal(t *testing.T) {
	m := &Mock{Terminal: true}
	if !m.IsTerminal() {
		t.Error("expected IsTerminal() = true")
	}
	m.Terminal = false
	if m.IsTerminal() {
		t.Error("expected IsTerminal() = false")
	}
}

func TestMock_ReadHidden(t *testing.T) {
	m := &Mock{HiddenVal: "secret123"}
	got, err := m.ReadHidden("Enter code: ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "secret123" {
		t.Errorf("ReadHidden() = %q, want %q", got, "secret123")
	}
}

func TestMock_ReadHidden_Error(t *testing.T) {
	m := &Mock{Err: io.ErrUnexpectedEOF}
	_, err := m.ReadHidden("prompt: ")
	if err != io.ErrUnexpectedEOF {
		t.Errorf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestMock_ReadLine_Sequential(t *testing.T) {
	m := &Mock{Lines: []string{"first", "second", "third"}}
	for i, want := range []string{"first", "second", "third"} {
		got, err := m.ReadLine("prompt: ")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if got != want {
			t.Errorf("call %d: got %q, want %q", i, got, want)
		}
	}
}

func TestMock_ReadLine_BeyondLines(t *testing.T) {
	m := &Mock{Lines: []string{"only"}}
	_, _ = m.ReadLine("prompt: ")
	got, err := m.ReadLine("prompt: ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q past end of Lines, want empty string", got)
	}
}

func TestMock_ReadLine_Error(t *testing.T) {
	m := &Mock{Err: io.EOF}
	_, err := m.ReadLine("prompt: ")
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

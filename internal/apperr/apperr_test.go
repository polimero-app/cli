package apperr_test

import (
	"errors"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
)

func TestNew(t *testing.T) {
	err := apperr.New(2, "validation failed")
	if err.Code != 2 {
		t.Errorf("Code = %d, want 2", err.Code)
	}
	if err.Error() != "validation failed" {
		t.Errorf("Error() = %q, want %q", err.Error(), "validation failed")
	}
}

func TestNewf(t *testing.T) {
	err := apperr.Newf(3, "secret store error: %s", "unavailable")
	if err.Code != 3 {
		t.Errorf("Code = %d, want 3", err.Code)
	}
	if err.Error() != "secret store error: unavailable" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestExitError_IsError(t *testing.T) {
	var err error = apperr.New(1, "failure")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Error("expected errors.As to match *ExitError")
	}
	if exitErr.Code != 1 {
		t.Errorf("Code = %d, want 1", exitErr.Code)
	}
}

func TestNew_EmptyMessage(t *testing.T) {
	err := apperr.New(1, "")
	if err.Error() == "" {
		t.Error("Error() should not be empty even with empty Msg")
	}
}

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

func TestWrap_PreservesCause(t *testing.T) {
	cause := errors.New("underlying failure")
	err := apperr.Wrap(3, "cannot access keychain", cause)
	if err.Code != 3 {
		t.Errorf("Code = %d, want 3", err.Code)
	}
	if err.Error() != "cannot access keychain" {
		t.Errorf("Error() = %q, want %q", err.Error(), "cannot access keychain")
	}
	if !errors.Is(err, cause) {
		t.Error("expected errors.Is(err, cause) to be true")
	}
}

func TestWrap_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := apperr.Wrap(4, "network failure", cause)
	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}
}

func TestWrap_NilCause(t *testing.T) {
	err := apperr.Wrap(1, "no cause", nil)
	if err.Code != 1 {
		t.Errorf("Code = %d, want 1", err.Code)
	}
	if errors.Unwrap(err) != nil {
		t.Error("expected Unwrap() to return nil for nil cause")
	}
}

func TestErrorsAs_ThroughWrap(t *testing.T) {
	cause := apperr.New(2, "inner error")
	outer := apperr.Wrap(4, "outer error", cause)
	var exitErr *apperr.ExitError
	if !errors.As(outer, &exitErr) {
		t.Fatal("expected errors.As to match outer *ExitError")
	}
	if exitErr.Code != 4 {
		t.Errorf("Code = %d, want 4 (outer)", exitErr.Code)
	}
}

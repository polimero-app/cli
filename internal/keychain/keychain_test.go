package keychain_test

import (
	"errors"
	"testing"

	"github.com/polimero-app/cli/internal/keychain"
)

func TestMock_SetGetDelete(t *testing.T) {
	kc := keychain.NewMock()

	if _, err := kc.Get("svc", "acc"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on empty store, got %v", err)
	}

	if err := kc.Set("svc", "acc", "secret"); err != nil {
		t.Fatal(err)
	}

	v, err := kc.Get("svc", "acc")
	if err != nil {
		t.Fatal(err)
	}
	if v != "secret" {
		t.Errorf("got %q, want %q", v, "secret")
	}

	if err := kc.Delete("svc", "acc"); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get("svc", "acc"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMock_DeleteNotFound(t *testing.T) {
	kc := keychain.NewMock()
	if err := kc.Delete("svc", "missing"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMock_ServiceIsolation(t *testing.T) {
	kc := keychain.NewMock()
	_ = kc.Set("svc1", "acc", "s1")
	_ = kc.Set("svc2", "acc", "s2")
	v1, _ := kc.Get("svc1", "acc")
	v2, _ := kc.Get("svc2", "acc")
	if v1 != "s1" || v2 != "s2" {
		t.Errorf("service isolation broken: got %q, %q", v1, v2)
	}
}

func TestMock_AccountIsolation(t *testing.T) {
	kc := keychain.NewMock()
	_ = kc.Set("svc", "acc1", "a")
	_ = kc.Set("svc", "acc2", "b")
	v1, _ := kc.Get("svc", "acc1")
	v2, _ := kc.Get("svc", "acc2")
	if v1 != "a" || v2 != "b" {
		t.Errorf("account isolation broken: got %q, %q", v1, v2)
	}
}

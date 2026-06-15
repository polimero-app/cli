package keychain_test

import (
	"context"
	"errors"
	"testing"

	"github.com/polimero-app/cli/internal/keychain"
)

func TestMock_SetGetDelete(t *testing.T) {
	ctx := context.Background()
	kc := keychain.NewMock()

	if _, err := kc.Get(ctx, "svc", "acc"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on empty store, got %v", err)
	}

	if err := kc.Set(ctx, "svc", "acc", "secret"); err != nil {
		t.Fatal(err)
	}

	v, err := kc.Get(ctx, "svc", "acc")
	if err != nil {
		t.Fatal(err)
	}
	if v != "secret" {
		t.Errorf("got %q, want %q", v, "secret")
	}

	if err := kc.Delete(ctx, "svc", "acc"); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get(ctx, "svc", "acc"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMock_DeleteNotFound(t *testing.T) {
	kc := keychain.NewMock()
	if err := kc.Delete(context.Background(), "svc", "missing"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMock_ServiceIsolation(t *testing.T) {
	ctx := context.Background()
	kc := keychain.NewMock()
	_ = kc.Set(ctx, "svc1", "acc", "s1")
	_ = kc.Set(ctx, "svc2", "acc", "s2")
	v1, _ := kc.Get(ctx, "svc1", "acc")
	v2, _ := kc.Get(ctx, "svc2", "acc")
	if v1 != "s1" || v2 != "s2" {
		t.Errorf("service isolation broken: got %q, %q", v1, v2)
	}
}

func TestMock_AccountIsolation(t *testing.T) {
	ctx := context.Background()
	kc := keychain.NewMock()
	_ = kc.Set(ctx, "svc", "acc1", "a")
	_ = kc.Set(ctx, "svc", "acc2", "b")
	v1, _ := kc.Get(ctx, "svc", "acc1")
	v2, _ := kc.Get(ctx, "svc", "acc2")
	if v1 != "a" || v2 != "b" {
		t.Errorf("account isolation broken: got %q, %q", v1, v2)
	}
}

func TestMock_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	kc := keychain.NewMock()
	if err := kc.Set(ctx, "svc", "acc", "secret"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set error = %v, want context.Canceled", err)
	}
	if _, err := kc.Get(ctx, "svc", "acc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
	if err := kc.Delete(ctx, "svc", "acc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete error = %v, want context.Canceled", err)
	}
}

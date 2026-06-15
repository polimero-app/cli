package keychain

import (
	"context"
	"errors"

	gokeyring "github.com/zalando/go-keyring"
)

// Real wraps go-keyring for OS-native secret storage.
type Real struct{}

// NewReal returns a Real keychain backed by the OS secret store.
func NewReal() *Real { return &Real{} }

func (r *Real) Get(ctx context.Context, service, account string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	type result struct {
		value string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := gokeyring.Get(service, account)
		if err != nil && errors.Is(err, gokeyring.ErrNotFound) {
			err = ErrNotFound
		}
		ch <- result{value: v, err: err}
	}()
	select {
	case res := <-ch:
		return res.value, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (r *Real) Set(ctx context.Context, service, account, secret string) error {
	return runWithContext(ctx, func() error {
		return gokeyring.Set(service, account, secret)
	})
}

func (r *Real) Delete(ctx context.Context, service, account string) error {
	return runWithContext(ctx, func() error {
		err := gokeyring.Delete(service, account)
		if errors.Is(err, gokeyring.ErrNotFound) {
			return ErrNotFound
		}
		return err
	})
}

func runWithContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ch := make(chan error, 1)
	go func() {
		ch <- fn()
	}()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

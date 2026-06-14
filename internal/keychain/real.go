package keychain

import (
	"errors"

	gokeyring "github.com/zalando/go-keyring"
)

// Real wraps go-keyring for OS-native secret storage.
type Real struct{}

// NewReal returns a Real keychain backed by the OS secret store.
func NewReal() *Real { return &Real{} }

func (r *Real) Get(service, account string) (string, error) {
	v, err := gokeyring.Get(service, account)
	if err != nil {
		if errors.Is(err, gokeyring.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	return v, nil
}

func (r *Real) Set(service, account, secret string) error {
	return gokeyring.Set(service, account, secret)
}

func (r *Real) Delete(service, account string) error {
	err := gokeyring.Delete(service, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

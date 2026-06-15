package keychain

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get and Delete when the account does not exist.
var ErrNotFound = errors.New("secret not found")

// Keychain abstracts OS secret store access.
// All methods accept a service and account identifier.
// Service is always "polimero"; account format is "<driver>:<profile>:<key>".
type Keychain interface {
	Get(ctx context.Context, service, account string) (string, error)
	Set(ctx context.Context, service, account, secret string) error
	Delete(ctx context.Context, service, account string) error
}

package keychain

import "errors"

// ErrNotFound is returned by Get and Delete when the account does not exist.
var ErrNotFound = errors.New("secret not found")

// Keychain abstracts OS secret store access.
// All methods accept a service and account identifier.
// Service is always "polimero"; account format is "<driver>:<profile>:<key>".
type Keychain interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

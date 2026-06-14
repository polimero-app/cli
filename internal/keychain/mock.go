package keychain

import "sync"

// Mock is an in-memory Keychain implementation for use in tests.
type Mock struct {
	mu   sync.Mutex
	data map[string]string
}

// NewMock returns an empty in-memory keychain.
// It is exported for use by command-layer tests and is safe to include in binaries
// (no OS calls, no sensitive data, just an in-memory map).
func NewMock() *Mock {
	return &Mock{data: make(map[string]string)}
}

func storeKey(service, account string) string { return service + "\x00" + account }

func (m *Mock) Get(service, account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[storeKey(service, account)]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Mock) Set(service, account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[storeKey(service, account)] = secret
	return nil
}

func (m *Mock) Delete(service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := storeKey(service, account)
	if _, ok := m.data[k]; !ok {
		return ErrNotFound
	}
	delete(m.data, k)
	return nil
}

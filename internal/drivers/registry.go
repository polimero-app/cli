package drivers

import (
	"sort"

	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers/bambulan"
)

var registry = map[string]driver.Driver{
	"bambu-lan": bambulan.New(),
}

// Get returns the driver registered under name and true, or (nil, false) if not found.
func Get(name string) (driver.Driver, bool) {
	d, ok := registry[name]
	return d, ok
}

// Names returns all registered driver names in alphabetical order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

package drivers

import (
	"sort"

	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers/bambulan"
	"github.com/polimero-app/cli/internal/drivers/moonraker"
)

// Info describes a registered driver for user-facing listings.
type Info struct {
	Name        string
	Description string
}

type registration struct {
	driver      driver.Driver
	description string
}

var registry = map[string]registration{
	"bambu-lan": {
		driver:      bambulan.New(),
		description: "Bambu Lab printers over LAN mode",
	},
	"moonraker": {
		driver:      moonraker.New(),
		description: "Moonraker-compatible Klipper printers",
	},
}

// Get returns the driver registered under name and true, or (nil, false) if not found.
func Get(name string) (driver.Driver, bool) {
	reg, ok := registry[name]
	return reg.driver, ok
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

// All returns all registered driver instances in alphabetical order by name.
func All() []driver.Driver {
	names := Names()
	out := make([]driver.Driver, 0, len(names))
	for _, name := range names {
		out = append(out, registry[name].driver)
	}
	return out
}

// List returns registered driver metadata in alphabetical order by name.
func List() []Info {
	names := Names()
	out := make([]Info, 0, len(names))
	for _, name := range names {
		reg := registry[name]
		out = append(out, Info{
			Name:        name,
			Description: reg.description,
		})
	}
	return out
}

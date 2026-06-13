package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

const currentVersion = 1

var (
	ErrUnsupportedVersion = errors.New("unsupported config schema version")
	ErrMalformed          = errors.New("malformed config file")
)

// Profile holds the non-secret settings for a named printer.
// Name is populated from the YAML map key after loading.
// Serial is driver-specific (required for bambu-lan, empty for drivers that don't use it).
type Profile struct {
	Name     string    `yaml:"-"`
	Driver   string    `yaml:"driver"`
	Host     string    `yaml:"host"`
	Serial   string    `yaml:"serial,omitempty"`
	Timeout  string    `yaml:"timeout"`
	Insecure bool      `yaml:"insecure"`
	Created  time.Time `yaml:"created"`
	Updated  time.Time `yaml:"updated"`
}

type Config struct {
	profiles map[string]Profile
}

type configFile struct {
	Version  int                `yaml:"version"`
	Profiles map[string]Profile `yaml:"profiles"`
}

// Open loads config from dir/polimero.yaml.
// Returns an empty Config if the file or directory does not exist.
// Returns ErrUnsupportedVersion if the version field is not 1.
// Returns ErrMalformed if the YAML cannot be parsed.
func Open(dir string) (*Config, error) {
	path := filepath.Join(dir, "polimero.yaml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{profiles: make(map[string]Profile)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var f configFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMalformed, err)
	}

	if f.Version != currentVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, f.Version, currentVersion)
	}

	if f.Profiles == nil {
		f.Profiles = make(map[string]Profile)
	}

	return &Config{profiles: f.Profiles}, nil
}

// Load loads config from the OS default config dir.
// POLIMERO_CONFIG_DIR env var overrides the default (used in tests).
func Load() (*Config, error) {
	dir := os.Getenv("POLIMERO_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("locating config directory: %w", err)
		}
		dir = filepath.Join(base, "polimero")
	}
	return Open(dir)
}

// SortedProfiles returns all profiles sorted alphabetically by name.
// Each returned Profile has its Name field populated.
func (c *Config) SortedProfiles() []Profile {
	names := make([]string, 0, len(c.profiles))
	for name := range c.profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Profile, 0, len(names))
	for _, name := range names {
		p := c.profiles[name]
		p.Name = name
		out = append(out, p)
	}
	return out
}

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
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
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

var (
	// ErrProfileAlreadyExists is returned by AddProfile when the name is taken.
	ErrProfileAlreadyExists = errors.New("profile already exists")
	// ErrProfileNotFound is returned by RemoveProfile when the name is absent.
	ErrProfileNotFound = errors.New("profile not found")
)

// ConfigDir resolves the config directory.
// POLIMERO_CONFIG_DIR env var overrides os.UserConfigDir()/polimero.
func ConfigDir() (string, error) {
	if d := os.Getenv("POLIMERO_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config directory: %w", err)
	}
	return filepath.Join(base, "polimero"), nil
}

// GetProfile returns the named profile and true if found.
func (c *Config) GetProfile(name string) (Profile, bool) {
	p, ok := c.profiles[name]
	return p, ok
}

// AddProfile inserts a new profile. Returns ErrProfileAlreadyExists if the name is taken.
func (c *Config) AddProfile(name string, p Profile) error {
	if _, exists := c.profiles[name]; exists {
		return ErrProfileAlreadyExists
	}
	c.profiles[name] = p
	return nil
}

// RemoveProfile deletes the named profile and returns it.
// Returns ErrProfileNotFound if the name is absent.
func (c *Config) RemoveProfile(name string) (Profile, error) {
	p, ok := c.profiles[name]
	if !ok {
		return Profile{}, ErrProfileNotFound
	}
	delete(c.profiles, name)
	return p, nil
}

// Save atomically writes the config to dir/polimero.yaml with 0600 permissions.
// Creates dir if it does not exist (0700). Uses write-to-temp + rename for atomicity.
func Save(dir string, c *Config) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(configFile{Version: currentVersion, Profiles: c.profiles})
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".polimero-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeds

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting temp file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	return os.Rename(tmpName, filepath.Join(dir, "polimero.yaml"))
}

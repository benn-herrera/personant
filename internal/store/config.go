package store

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"personant/internal/memops"
)

// LoadConfig reads config.toml at path. A nonexistent file yields a
// zero Config and a nil error — a fresh home may have no config.toml,
// in which case all choices fall back to their defaults.
func LoadConfig(path string) (memops.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return memops.Config{}, nil
		}
		return memops.Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg memops.Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return memops.Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}

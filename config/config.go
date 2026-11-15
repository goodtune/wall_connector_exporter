// Package config reads the configuration file of the exporter. The file
// lists the chargers that the exporter polls.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/goodtune/wall_connector_exporter/wallconnector"
)

// MinPollInterval is the shortest permitted time between two polls of a
// charger. A charger must not be polled faster than this.
const MinPollInterval = 5 * time.Second

// DefaultName is the name of a charger that has no name.
const DefaultName = "wall_connector"

// Config is the content of the configuration file.
type Config struct {
	Devices []Device `yaml:"devices"`
}

// Device is one charger that the exporter polls.
type Device struct {
	// Name is the value of the device label of all metrics of the charger.
	Name string `yaml:"name"`
	// Address is the host or the host:port of the charger. Without a port,
	// the exporter uses port 80.
	Address string `yaml:"address"`
	// PollInterval is the shortest time between two polls of the charger.
	PollInterval time.Duration `yaml:"poll_interval"`
	// Timeout is the timeout of one HTTP request.
	Timeout time.Duration `yaml:"timeout"`
	// RequestPause is the pause between two HTTP requests of one poll.
	RequestPause time.Duration `yaml:"request_pause"`
	// SplitPhase says that the charger has a split-phase supply. It changes
	// how the exporter calculates the charging power.
	SplitPhase bool `yaml:"split_phase"`
}

// Defaults are the values for the settings that a device does not set.
type Defaults struct {
	PollInterval time.Duration
	Timeout      time.Duration
	RequestPause time.Duration
}

// Load reads and validates the configuration file at path.
func Load(path string, defaults Defaults) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := Parse(data, defaults)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Parse reads a configuration from YAML and validates it. A field that the
// format does not have is an error.
func Parse(data []byte, defaults Defaults) (*Config, error) {
	c := &Config{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(c); err != nil {
		return nil, err
	}
	if err := c.Validate(defaults); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate applies the defaults and reports settings that are not correct
// or not safe for a charger.
func (c *Config) Validate(defaults Defaults) error {
	if len(c.Devices) == 0 {
		return errors.New("the configuration has no devices")
	}
	names := map[string]bool{}
	addresses := map[string]bool{}
	for i := range c.Devices {
		d := &c.Devices[i]
		if d.Name == "" {
			d.Name = DefaultName
		}
		if d.PollInterval == 0 {
			d.PollInterval = defaults.PollInterval
		}
		if d.Timeout == 0 {
			d.Timeout = defaults.Timeout
		}
		if d.RequestPause == 0 {
			d.RequestPause = defaults.RequestPause
		}
		what := fmt.Sprintf("device %d (%q)", i+1, d.Name)
		if names[d.Name] {
			return fmt.Errorf("%s: the name is used two times", what)
		}
		names[d.Name] = true
		hostPort, err := wallconnector.HostPort(d.Address)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		// Two polls of one charger must not run at the same time.
		if addresses[hostPort] {
			return fmt.Errorf("%s: the address is used two times", what)
		}
		addresses[hostPort] = true
		if d.PollInterval < MinPollInterval {
			return fmt.Errorf("%s: the poll interval must be %s or more", what, MinPollInterval)
		}
		if d.Timeout <= 0 || d.RequestPause < 0 {
			return fmt.Errorf("%s: the timeout must be more than 0 and the request pause must not be negative", what)
		}
	}
	return nil
}

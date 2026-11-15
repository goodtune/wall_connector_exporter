package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var defaults = Defaults{PollInterval: 10 * time.Second, Timeout: 5 * time.Second, RequestPause: 100 * time.Millisecond}

func TestParse(t *testing.T) {
	c, err := Parse([]byte(`
devices:
  - address: 192.0.2.10
  - name: carport
    address: "[2001:db8::10]:8080"
    poll_interval: 30s
    timeout: 2s
    request_pause: 250ms
    split_phase: true
`), defaults)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(c.Devices))
	}
	first, second := c.Devices[0], c.Devices[1]
	if first.Name != DefaultName || first.PollInterval != 10*time.Second || first.Timeout != 5*time.Second ||
		first.RequestPause != 100*time.Millisecond || first.SplitPhase {
		t.Errorf("the first device did not get the defaults: %+v", first)
	}
	if second.Name != "carport" || second.PollInterval != 30*time.Second || second.Timeout != 2*time.Second ||
		second.RequestPause != 250*time.Millisecond || !second.SplitPhase {
		t.Errorf("the second device lost its settings: %+v", second)
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"no devices":     `devices: []`,
		"empty file":     ``,
		"unknown field":  "devices:\n  - {address: 192.0.2.10, write: true}",
		"no address":     "devices:\n  - {name: garage}",
		"address is URL": "devices:\n  - {address: \"http://192.0.2.10/\"}",
		"empty port":     "devices:\n  - {address: \"192.0.2.10:\"}",
		"fast poll":      "devices:\n  - {address: 192.0.2.10, poll_interval: 1s}",
		"negative pause": "devices:\n  - {address: 192.0.2.10, request_pause: -1s}",
		"bad duration":   "devices:\n  - {address: 192.0.2.10, timeout: soon}",
		"same name":      "devices:\n  - {name: a, address: 192.0.2.10}\n  - {name: a, address: 192.0.2.11}",
		"same default":   "devices:\n  - {address: 192.0.2.10}\n  - {address: 192.0.2.11}",
		"same address":   "devices:\n  - {name: a, address: 192.0.2.10}\n  - {name: b, address: \"192.0.2.10:80\"}",
	}
	for name, text := range tests {
		if _, err := Parse([]byte(text), defaults); err == nil {
			t.Errorf("%s: Parse returned no error", name)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("devices:\n  - {address: 192.0.2.10}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, defaults); err != nil {
		t.Errorf("Load: %v", err)
	}
	if _, err := Load(path+".missing", defaults); err == nil {
		t.Error("Load of a missing file returned no error")
	}
}

// The example configuration in the repository must be valid.
func TestExampleConfig(t *testing.T) {
	c, err := Load("../examples/config.yml", defaults)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Devices) != 2 || !c.Devices[1].SplitPhase {
		t.Errorf("the example has not the expected devices: %+v", c.Devices)
	}
}

// The configuration file of the packages has no devices: the exporter must
// refuse it, and the service then does not start again.
func TestPackagedConfigHasNoDevices(t *testing.T) {
	if _, err := Load("../packaging/config.yml", defaults); err == nil {
		t.Error("Load of the packaged configuration returned no error")
	}
}

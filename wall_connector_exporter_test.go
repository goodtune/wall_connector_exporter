package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/goodtune/wall_connector_exporter/config"
)

var testDefaults = config.Defaults{PollInterval: 10 * time.Second, Timeout: 5 * time.Second, RequestPause: 100 * time.Millisecond}

func TestSettingsDevices(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(file, []byte("devices:\n  - {name: garage, address: 192.0.2.10}\n  - {name: carport, address: 192.0.2.11}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	devices, err := settings{address: "192.0.2.10", deviceName: "garage", splitPhase: true, defaults: testDefaults}.devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Name != "garage" || !devices[0].SplitPhase || devices[0].PollInterval != 10*time.Second {
		t.Errorf("the device of the flags is not correct: %+v", devices)
	}

	devices, err = settings{configFile: file, defaults: testDefaults}.devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[1].Name != "carport" {
		t.Errorf("the devices of the file are not correct: %+v", devices)
	}

	for name, s := range map[string]settings{
		"no charger":        {defaults: testDefaults},
		"file and address":  {configFile: file, address: "192.0.2.10", defaults: testDefaults},
		"missing file":      {configFile: file + ".missing", defaults: testDefaults},
		"address is a URL":  {address: "http://192.0.2.10", defaults: testDefaults},
		"poll is too fast":  {address: "192.0.2.10", defaults: config.Defaults{PollInterval: time.Second, Timeout: time.Second}},
		"timeout is absent": {address: "192.0.2.10", defaults: config.Defaults{PollInterval: 10 * time.Second}},
	} {
		if _, err := s.devices(); err == nil {
			t.Errorf("%s: devices returned no error", name)
		}
	}
}

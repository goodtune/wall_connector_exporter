package wallconnector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fixture reads a file of testdata. The values of the files are not from a
// real charger.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func value[T any](t *testing.T, name string, p *T) T {
	t.Helper()
	if p == nil {
		t.Fatalf("%s was not decoded", name)
	}
	return *p
}

func TestDecodeVitals(t *testing.T) {
	v := &Vitals{}
	if err := decode(fixture(t, "vitals_charging.json"), v); err != nil {
		t.Fatal(err)
	}
	if !value(t, "contactor_closed", v.ContactorClosed) || !value(t, "vehicle_connected", v.VehicleConnected) {
		t.Error("contactor_closed and vehicle_connected must be true")
	}
	numbers := map[string]struct {
		got  *float64
		want float64
	}{
		"session_s":           {v.SessionSeconds, 3600},
		"session_energy_wh":   {v.SessionEnergyWattHours, 11000.5},
		"grid_v":              {v.GridVolts, 230},
		"grid_hz":             {v.GridHertz, 49.95},
		"vehicle_current_a":   {v.VehicleCurrentAmperes, 16},
		"currentA_a":          {v.CurrentAAmperes, 16},
		"currentB_a":          {v.CurrentBAmperes, 15.5},
		"currentC_a":          {v.CurrentCAmperes, 16.5},
		"currentN_a":          {v.CurrentNAmperes, 0.5},
		"voltageA_v":          {v.VoltageAVolts, 230},
		"voltageB_v":          {v.VoltageBVolts, 232},
		"voltageC_v":          {v.VoltageCVolts, 228},
		"relay_k1_v":          {v.RelayK1Volts, 6},
		"relay_k2_v":          {v.RelayK2Volts, 6},
		"pcba_temp_c":         {v.PCBACelsius, 35},
		"handle_temp_c":       {v.HandleCelsius, 30.5},
		"mcu_temp_c":          {v.MCUCelsius, 42},
		"uptime_s":            {v.UptimeSeconds, 90000},
		"input_thermopile_uv": {v.InputThermopileMicrovolts, -400},
		"prox_v":              {v.ProximityVolts, 1.5},
		"pilot_high_v":        {v.PilotHighVolts, 5.9},
		"pilot_low_v":         {v.PilotLowVolts, -11.8},
		"config_status":       {v.ConfigStatus, 5},
		"evse_state":          {v.EVSEState, 11},
	}
	for name, n := range numbers {
		if got := value(t, name, n.got); got != n.want {
			t.Errorf("%s = %v, want %v", name, got, n.want)
		}
	}
	if v.RelayCoilVolts != nil {
		t.Error("relay_coil_v is not in the response and must be nil")
	}
	if v.CurrentAlerts == nil || len(v.CurrentAlerts) != 0 {
		t.Errorf("current_alerts = %v, want an empty list", v.CurrentAlerts)
	}

	idle := &Vitals{}
	if err := decode(fixture(t, "vitals_idle.json"), idle); err != nil {
		t.Fatal(err)
	}
	if value(t, "contactor_closed", idle.ContactorClosed) || value(t, "vehicle_connected", idle.VehicleConnected) {
		t.Error("contactor_closed and vehicle_connected must be false")
	}
	if len(idle.EVSENotReadyReasons) != 2 || idle.EVSENotReadyReasons[0] != 4 || idle.EVSENotReadyReasons[1] != 8 {
		t.Errorf("evse_not_ready_reasons = %v, want [4 8]", idle.EVSENotReadyReasons)
	}
}

func TestDecodeOtherEndpoints(t *testing.T) {
	l := &Lifetime{}
	if err := decode(fixture(t, "lifetime.json"), l); err != nil {
		t.Fatal(err)
	}
	numbers := map[string]struct {
		got  *float64
		want float64
	}{
		"contactor_cycles":        {l.ContactorCycles, 120},
		"contactor_cycles_loaded": {l.ContactorCyclesLoaded, 3},
		"alert_count":             {l.AlertCount, 4500},
		"thermal_foldbacks":       {l.ThermalFoldbacks, 1},
		"avg_startup_temp":        {l.AvgStartupCelsius, 25.5},
		"charge_starts":           {l.ChargeStarts, 118},
		"energy_wh":               {l.EnergyWattHours, 1234567},
		"connector_cycles":        {l.ConnectorCycles, 60},
		"uptime_s":                {l.UptimeSeconds, 20000000},
		"charging_time_s":         {l.ChargingSeconds, 500000},
	}
	for name, n := range numbers {
		if got := value(t, name, n.got); got != n.want {
			t.Errorf("%s = %v, want %v", name, got, n.want)
		}
	}

	w := &WifiStatus{}
	if err := decode(fixture(t, "wifi_status.json"), w); err != nil {
		t.Fatal(err)
	}
	if value(t, "wifi_signal_strength", w.SignalStrength) != 60 || value(t, "wifi_rssi", w.RSSI) != -58 ||
		value(t, "wifi_snr", w.SNR) != 35 || !value(t, "wifi_connected", w.Connected) || !value(t, "internet", w.Internet) {
		t.Errorf("wifi_status was not decoded correctly: %+v", w)
	}

	v := &Version{}
	if err := decode(fixture(t, "version.json"), v); err != nil {
		t.Fatal(err)
	}
	if value(t, "firmware_version", v.FirmwareVersion) != "1.2.3+g0123456789abcd" || value(t, "part_number", v.PartNumber) != "0000000-00-A" {
		t.Errorf("version was not decoded correctly: %+v", v)
	}
}

// The field set changes with the firmware version. Decoding must accept a
// response with fewer fields, with more fields, and with a field of a
// different type.
func TestDecodeTolerance(t *testing.T) {
	v := &Vitals{}
	if err := decode([]byte(`{}`), v); err != nil {
		t.Fatalf("empty object: %v", err)
	}
	if v.GridVolts != nil || v.ContactorClosed != nil || v.CurrentAlerts != nil || v.EVSENotReadyReasons != nil {
		t.Errorf("an empty object must leave all fields nil: %+v", v)
	}

	v = &Vitals{}
	if err := decode([]byte(`{"grid_v": 229.5, "relay_coil_v": 11.5}`), v); err != nil {
		t.Fatalf("few fields: %v", err)
	}
	if value(t, "grid_v", v.GridVolts) != 229.5 || value(t, "relay_coil_v", v.RelayCoilVolts) != 11.5 || v.GridHertz != nil || v.RelayK1Volts != nil {
		t.Errorf("few fields: %+v", v)
	}

	v = &Vitals{}
	if err := decode([]byte(`{"new_field": {"a": [1, 2]}, "grid_v": 229.5, "another": "text", "evse_state": null}`), v); err != nil {
		t.Fatalf("extra fields: %v", err)
	}
	if value(t, "grid_v", v.GridVolts) != 229.5 || v.EVSEState != nil {
		t.Errorf("extra fields: %+v", v)
	}

	v = &Vitals{}
	if err := decode([]byte(`{"grid_v": "high", "grid_hz": 50.1, "contactor_closed": 1, "vehicle_connected": true, "current_alerts": ["a", {"b": 1}, 3], "evse_not_ready_reasons": [4, "x"]}`), v); err != nil {
		t.Fatalf("field of a different type: %v", err)
	}
	if v.GridVolts != nil || v.ContactorClosed != nil || v.EVSENotReadyReasons != nil {
		t.Errorf("a field of a different type must stay nil: %+v", v)
	}
	if value(t, "grid_hz", v.GridHertz) != 50.1 || !value(t, "vehicle_connected", v.VehicleConnected) || len(v.CurrentAlerts) != 3 {
		t.Errorf("the fields after a field of a different type were not decoded: %+v", v)
	}

	for name, text := range map[string]string{
		"empty":     ``,
		"null":      `null`,
		"list":      `[1, 2]`,
		"text":      `<html>busy</html>`,
		"truncated": `{"grid_v": 229.5, "grid_hz"`,
	} {
		if err := decode([]byte(text), &Vitals{}); err == nil {
			t.Errorf("%s: decode returned no error", name)
		}
	}
}

func TestHostPort(t *testing.T) {
	good := map[string]string{
		"192.0.2.10":           "192.0.2.10:80",
		"192.0.2.10:8080":      "192.0.2.10:8080",
		"charger.example.com":  "charger.example.com:80",
		"charger.example:81":   "charger.example:81",
		"2001:db8::10":         "[2001:db8::10]:80",
		"[2001:db8::10]":       "[2001:db8::10]:80",
		"[2001:db8::10]:8080":  "[2001:db8::10]:8080",
		"localhost":            "localhost:80",
		"wall-connector.local": "wall-connector.local:80",
	}
	for address, want := range good {
		got, err := HostPort(address)
		if err != nil || got != want {
			t.Errorf("HostPort(%q) = %q, %v, want %q", address, got, err, want)
		}
	}
	for _, address := range []string{"", "http://192.0.2.10", "192.0.2.10/api", "192.0.2.10:", ":80", "user@192.0.2.10", "a b", "192.0.2.10:port"} {
		if got, err := HostPort(address); err == nil {
			t.Errorf("HostPort(%q) = %q, want an error", address, got)
		}
	}
}

// server is a charger for the tests. It counts the requests and fails the
// test for a request that is not a GET of an endpoint of the API.
func server(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	requests := &atomic.Int64{}
	files := map[string]string{
		pathVitals:     "vitals_idle.json",
		pathLifetime:   "lifetime.json",
		pathWifiStatus: "wifi_status.json",
		pathVersion:    "version.json",
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("the client sent the method %s", r.Method)
		}
		if r.ContentLength > 0 || r.URL.RawQuery != "" {
			t.Errorf("the client sent a body or a query: %s", r.URL)
		}
		file, ok := files[r.URL.Path]
		if !ok {
			t.Errorf("the client asked for %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, file))
	}))
	t.Cleanup(s.Close)
	return s, requests
}

func client(t *testing.T, s *httptest.Server, timeout time.Duration) *Client {
	t.Helper()
	c, err := NewClient(strings.TrimPrefix(s.URL, "http://"), timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestClient(t *testing.T) {
	s, requests := server(t, nil)
	c := client(t, s, time.Second)
	ctx := context.Background()

	v, err := c.Vitals(ctx)
	if err != nil || value(t, "grid_v", v.GridVolts) != 231.4 {
		t.Errorf("Vitals: %+v, %v", v, err)
	}
	l, err := c.Lifetime(ctx)
	if err != nil || value(t, "energy_wh", l.EnergyWattHours) != 1234567 {
		t.Errorf("Lifetime: %+v, %v", l, err)
	}
	w, err := c.WifiStatus(ctx)
	if err != nil || value(t, "wifi_rssi", w.RSSI) != -58 {
		t.Errorf("WifiStatus: %+v, %v", w, err)
	}
	ver, err := c.Version(ctx)
	if err != nil || value(t, "part_number", ver.PartNumber) != "0000000-00-A" {
		t.Errorf("Version: %+v, %v", ver, err)
	}
	if got := requests.Load(); got != 4 {
		t.Errorf("the client sent %d requests, want 4", got)
	}
}

func TestClientErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("status", func(t *testing.T) {
		s, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "busy", http.StatusServiceUnavailable)
		})
		_, err := client(t, s, time.Second).Vitals(ctx)
		if err == nil || errors.Is(err, ErrNoResponse) {
			t.Errorf("err = %v, want an error that is not ErrNoResponse", err)
		}
	})

	t.Run("not JSON", func(t *testing.T) {
		s, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>busy</html>"))
		})
		_, err := client(t, s, time.Second).Lifetime(ctx)
		if err == nil || errors.Is(err, ErrNoResponse) {
			t.Errorf("err = %v, want an error that is not ErrNoResponse", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		s, requests := server(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, pathLifetime, http.StatusFound)
		})
		_, err := client(t, s, time.Second).Vitals(ctx)
		if err == nil {
			t.Error("a redirect must be an error")
		}
		if got := requests.Load(); got != 1 {
			t.Errorf("the client sent %d requests, want 1: it must not follow a redirect", got)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		s, _ := server(t, func(http.ResponseWriter, *http.Request) {
			<-release
		})
		defer close(release)
		_, err := client(t, s, 50*time.Millisecond).Vitals(ctx)
		if !errors.Is(err, ErrNoResponse) {
			t.Errorf("err = %v, want ErrNoResponse", err)
		}
	})

	t.Run("no connection", func(t *testing.T) {
		s, _ := server(t, nil)
		c := client(t, s, time.Second)
		s.Close()
		_, err := c.Vitals(ctx)
		if !errors.Is(err, ErrNoResponse) {
			t.Errorf("err = %v, want ErrNoResponse", err)
		}
	})

	if _, err := NewClient("http://192.0.2.10/", time.Second); err == nil {
		t.Error("NewClient accepted a URL")
	}
}

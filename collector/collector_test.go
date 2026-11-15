package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/promslog"

	"github.com/goodtune/wall_connector_exporter/wallconnector"
)

// charger is an HTTP server that answers as a charger. The values that it
// gives are not from a real charger.
type charger struct {
	*httptest.Server

	mu sync.Mutex
	// files has the file of testdata for each path.
	files map[string]string
	// status has the HTTP status for the paths that must fail.
	status map[string]int

	requests atomic.Int64
	inFlight atomic.Int64
}

func newCharger(t *testing.T) *charger {
	t.Helper()
	c := &charger{
		files: map[string]string{
			"/api/1/vitals":      "vitals_idle.json",
			"/api/1/lifetime":    "lifetime.json",
			"/api/1/wifi_status": "wifi_status.json",
			"/api/1/version":     "version.json",
		},
		status: map[string]int{},
	}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.requests.Add(1)
		if c.inFlight.Add(1) > 1 {
			t.Error("two requests to one charger at the same time")
		}
		defer c.inFlight.Add(-1)
		if r.Method != http.MethodGet {
			t.Errorf("the exporter sent the method %s", r.Method)
		}
		c.mu.Lock()
		file, ok := c.files[r.URL.Path]
		status := c.status[r.URL.Path]
		c.mu.Unlock()
		if !ok {
			t.Errorf("the exporter asked for %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if status != 0 {
			http.Error(w, "failure of the test", status)
			return
		}
		data, err := os.ReadFile("../wallconnector/testdata/" + file)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *charger) set(path, file string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files[path] = file
}

func (c *charger) fail(path string, status int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status[path] = status
}

// device makes the device of the collector for a charger of the tests.
func (c *charger) device(t *testing.T, name string, interval time.Duration) Device {
	t.Helper()
	client, err := wallconnector.NewClient(strings.TrimPrefix(c.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return Device{Name: name, Reader: client, PollInterval: interval}
}

func newCollector(t *testing.T, devices ...Device) *Collector {
	t.Helper()
	c, err := New(devices, promslog.NewNopLogger())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// exposition returns the metrics of the collector in the text format.
func exposition(t *testing.T, c prometheus.Collector) string {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(c); err != nil {
		t.Fatal(err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var text strings.Builder
	for _, family := range families {
		if _, err := expfmt.MetricFamilyToText(&text, family); err != nil {
			t.Fatal(err)
		}
	}
	return text.String()
}

// wantSeries fails the test when a line of want is not in the exposition.
func wantSeries(t *testing.T, text string, want ...string) {
	t.Helper()
	lines := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		lines[line] = true
	}
	for _, line := range want {
		if !lines[line] {
			t.Errorf("the series is not in the output: %s", line)
		}
	}
}

// valueSeries returns the series that are values of a charger: all series
// but those of wall_connector_scrape_*.
func valueSeries(text string) []string {
	var series []string
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "wall_connector_scrape_") {
			continue
		}
		series = append(series, line)
	}
	return series
}

func TestCollectIdle(t *testing.T) {
	text := exposition(t, newCollector(t, newCharger(t).device(t, "garage", time.Minute)))
	wantSeries(t, text,
		`wall_connector_scrape_collector_success{collector="vitals",device="garage"} 1`,
		`wall_connector_scrape_collector_success{collector="lifetime",device="garage"} 1`,
		`wall_connector_scrape_collector_success{collector="wifi",device="garage"} 1`,
		`wall_connector_scrape_collector_success{collector="version",device="garage"} 1`,
		`wall_connector_device_info{device="garage",firmware_version="1.2.3+g0123456789abcd",part_number="0000000-00-A"} 1`,
		`wall_connector_vehicle_connected{device="garage"} 0`,
		`wall_connector_contactor_closed{device="garage"} 0`,
		`wall_connector_evse_state_code{device="garage"} 1`,
		`wall_connector_evse_state{device="garage",state="not_connected"} 1`,
		`wall_connector_config_status_code{device="garage"} 5`,
		`wall_connector_evse_not_ready_reason{device="garage",reason="4"} 1`,
		`wall_connector_evse_not_ready_reason{device="garage",reason="8"} 1`,
		`wall_connector_current_alerts{device="garage"} 0`,
		`wall_connector_session_energy_watt_hours{device="garage"} 0`,
		`wall_connector_session_duration_seconds{device="garage"} 0`,
		`wall_connector_vehicle_current_amperes{device="garage"} 0.4`,
		`wall_connector_phase_current_amperes{device="garage",phase="a"} 0.2`,
		`wall_connector_phase_current_amperes{device="garage",phase="b"} 0`,
		`wall_connector_phase_current_amperes{device="garage",phase="c"} 0.1`,
		`wall_connector_phase_current_amperes{device="garage",phase="n"} 0.1`,
		`wall_connector_phase_voltage_volts{device="garage",phase="a"} 0`,
		`wall_connector_phase_voltage_volts{device="garage",phase="b"} 2.5`,
		`wall_connector_phase_voltage_volts{device="garage",phase="c"} 0`,
		`wall_connector_charging_power_watts{device="garage"} 0`,
		`wall_connector_grid_voltage_volts{device="garage"} 231.4`,
		`wall_connector_grid_frequency_hertz{device="garage"} 50.012`,
		`wall_connector_temperature_celsius{device="garage",sensor="handle"} 19`,
		`wall_connector_temperature_celsius{device="garage",sensor="pcba"} 21.5`,
		`wall_connector_temperature_celsius{device="garage",sensor="mcu"} 28.25`,
		`wall_connector_pilot_voltage_volts{device="garage",level="high"} 11.9`,
		`wall_connector_pilot_voltage_volts{device="garage",level="low"} 11.9`,
		`wall_connector_proximity_voltage_volts{device="garage"} 0`,
		`wall_connector_relay_coil_voltage_volts{device="garage",relay="k1"} 12`,
		`wall_connector_relay_coil_voltage_volts{device="garage",relay="k2"} 0`,
		`wall_connector_input_thermopile_volts{device="garage"} -0.00015`,
		`wall_connector_uptime_seconds{device="garage"} 86400`,
		`wall_connector_lifetime_energy_watt_hours_total{device="garage"} 1.234567e+06`,
		`wall_connector_lifetime_charge_starts_total{device="garage"} 118`,
		`wall_connector_lifetime_charging_time_seconds_total{device="garage"} 500000`,
		`wall_connector_lifetime_uptime_seconds_total{device="garage"} 2e+07`,
		`wall_connector_lifetime_contactor_cycles_total{device="garage"} 120`,
		`wall_connector_lifetime_contactor_cycles_loaded_total{device="garage"} 3`,
		`wall_connector_lifetime_connector_cycles_total{device="garage"} 60`,
		`wall_connector_lifetime_alerts_total{device="garage"} 4500`,
		`wall_connector_lifetime_thermal_foldbacks_total{device="garage"} 1`,
		`wall_connector_lifetime_average_startup_temperature_celsius{device="garage"} 25.5`,
		`wall_connector_wifi_connected{device="garage"} 1`,
		`wall_connector_wifi_internet_connected{device="garage"} 1`,
		`wall_connector_wifi_signal_strength{device="garage"} 60`,
		`wall_connector_wifi_rssi_dbm{device="garage"} -58`,
		`wall_connector_wifi_snr_decibels{device="garage"} 35`,
	)
	// The number of series of a charger is fixed. Only the number of
	// evse_not_ready_reason series changes with the state.
	if got := len(valueSeries(text)); got != 47 {
		t.Errorf("the charger has %d value series, want 47", got)
	}
}

// The responses of a charger identify the charger and its network. The
// exporter must not export those fields. The test data has invented values
// for them.
func TestNoIdentifyingValues(t *testing.T) {
	text := strings.ToLower(exposition(t, newCollector(t, newCharger(t).device(t, "garage", time.Minute))))
	for _, private := range []string{
		"example0000001",       // serial_number
		"zxhhbxbszs1uzxr3b3jr", // wifi_ssid
		"00:00:5e:00:53:01",    // wifi_mac
		"192.0.2.10",           // wifi_infra_ip
		"service.example.com",  // web_service
		"serial", "ssid", "mac", "infra_ip",
	} {
		if strings.Contains(text, private) {
			t.Errorf("the output has %q", private)
		}
	}
}

func TestCollectCharging(t *testing.T) {
	ch := newCharger(t)
	ch.set("/api/1/vitals", "vitals_charging.json")

	text := exposition(t, newCollector(t, ch.device(t, "garage", time.Minute)))
	wantSeries(t, text,
		`wall_connector_vehicle_connected{device="garage"} 1`,
		`wall_connector_contactor_closed{device="garage"} 1`,
		`wall_connector_evse_state_code{device="garage"} 11`,
		`wall_connector_evse_state{device="garage",state="charging"} 1`,
		`wall_connector_session_energy_watt_hours{device="garage"} 11000.5`,
		`wall_connector_session_duration_seconds{device="garage"} 3600`,
		`wall_connector_vehicle_current_amperes{device="garage"} 16`,
		// 230 x 16 + 232 x 15.5 + 228 x 16.5
		`wall_connector_charging_power_watts{device="garage"} 11038`,
		`wall_connector_pilot_voltage_volts{device="garage",level="low"} -11.8`,
	)
	if strings.Contains(text, "wall_connector_evse_not_ready_reason{") {
		t.Error("a charger that is ready must have no evse_not_ready_reason series")
	}

	// Split phase: grid voltage times vehicle current, 230 x 16.
	split := ch.device(t, "garage", time.Minute)
	split.SplitPhase = true
	wantSeries(t, exposition(t, newCollector(t, split)),
		`wall_connector_charging_power_watts{device="garage"} 3680`)
}

func TestChargingPower(t *testing.T) {
	number := func(f float64) *float64 { return &f }
	closed, open := true, false
	full := wallconnector.Vitals{
		GridVolts: number(240), VehicleCurrentAmperes: number(10),
		VoltageAVolts: number(240), VoltageBVolts: number(1), VoltageCVolts: number(0),
		CurrentAAmperes: number(10), CurrentBAmperes: number(0), CurrentCAmperes: number(0.2),
	}
	tests := map[string]struct {
		change     func(*wallconnector.Vitals)
		splitPhase bool
		want       float64
		ok         bool
	}{
		"one phase in use":          {func(v *wallconnector.Vitals) { v.ContactorClosed = &closed }, false, 2400, true},
		"split phase":               {func(v *wallconnector.Vitals) { v.ContactorClosed = &closed }, true, 2400, true},
		"contactor open":            {func(v *wallconnector.Vitals) { v.ContactorClosed = &open }, false, 0, true},
		"contactor open, split":     {func(v *wallconnector.Vitals) { v.ContactorClosed = &open }, true, 0, true},
		"contactor not known":       {func(*wallconnector.Vitals) {}, false, 2400, true},
		"no voltage of a phase":     {func(v *wallconnector.Vitals) { v.VoltageBVolts = nil }, false, 0, false},
		"no current of a phase":     {func(v *wallconnector.Vitals) { v.CurrentCAmperes = nil }, false, 0, false},
		"split, no grid voltage":    {func(v *wallconnector.Vitals) { v.GridVolts = nil }, true, 0, false},
		"split, no vehicle current": {func(v *wallconnector.Vitals) { v.VehicleCurrentAmperes = nil }, true, 0, false},
	}
	for name, test := range tests {
		v := full
		test.change(&v)
		got, ok := ChargingPower(&v, test.splitPhase)
		if got != test.want || ok != test.ok {
			t.Errorf("%s: ChargingPower = %v, %v, want %v, %v", name, got, ok, test.want, test.ok)
		}
	}
}

func TestStateName(t *testing.T) {
	for code, want := range map[float64]string{
		0: "booting", 1: "not_connected", 2: "connected", 4: "ready", 6: "negotiating",
		8: "charging_finished", 9: "waiting_car", 10: "charging_reduced", 11: "charging",
		// The sources do not know these values, or do not agree.
		3: StateUnknown, 5: StateUnknown, 7: StateUnknown, 12: StateUnknown, 13: StateUnknown,
		-1: StateUnknown, 1.5: StateUnknown, 99: StateUnknown,
	} {
		if got := StateName(code); got != want {
			t.Errorf("StateName(%v) = %q, want %q", code, got, want)
		}
	}
}

// fakeReader gives fixed values without a network.
type fakeReader struct {
	mu      sync.Mutex
	vitals  wallconnector.Vitals
	err     error
	calls   int
	closed  int
	lastErr map[string]error
}

func (f *fakeReader) call(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := f.lastErr[name]; err != nil {
		return err
	}
	return f.err
}

func (f *fakeReader) Vitals(context.Context) (*wallconnector.Vitals, error) {
	v := f.vitals
	return &v, f.call("vitals")
}

func (f *fakeReader) Lifetime(context.Context) (*wallconnector.Lifetime, error) {
	return &wallconnector.Lifetime{}, f.call("lifetime")
}

func (f *fakeReader) WifiStatus(context.Context) (*wallconnector.WifiStatus, error) {
	return &wallconnector.WifiStatus{}, f.call("wifi")
}

func (f *fakeReader) Version(context.Context) (*wallconnector.Version, error) {
	return &wallconnector.Version{}, f.call("version")
}

func (f *fakeReader) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
}

// A state without a reliable name, a number that is two times in the list
// of reasons, the old relay field, and alerts of an unknown type.
func TestCollectUnknownValues(t *testing.T) {
	number := func(f float64) *float64 { return &f }
	reader := &fakeReader{vitals: wallconnector.Vitals{
		EVSEState:           number(7),
		RelayCoilVolts:      number(11.5),
		EVSENotReadyReasons: []int64{8, 4, 8},
		CurrentAlerts:       []json.RawMessage{[]byte(`"a"`), []byte(`{"b": 1}`)},
	}}
	text := exposition(t, newCollector(t, Device{Name: "garage", Reader: reader, PollInterval: time.Minute}))
	wantSeries(t, text,
		`wall_connector_evse_state_code{device="garage"} 7`,
		`wall_connector_evse_state{device="garage",state="unknown"} 1`,
		`wall_connector_evse_not_ready_reason{device="garage",reason="4"} 1`,
		`wall_connector_evse_not_ready_reason{device="garage",reason="8"} 1`,
		`wall_connector_current_alerts{device="garage"} 2`,
		`wall_connector_relay_coil_voltage_volts{device="garage",relay="coil"} 11.5`,
	)
	// The other fields are not there: the exporter makes no sample for
	// them, and no sample with the value 0.
	if got := valueSeries(text); len(got) != 6 {
		t.Errorf("got %d value series, want 6:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// A scrape must not always cause requests to the charger.
func TestPollInterval(t *testing.T) {
	ch := newCharger(t)
	c := newCollector(t, ch.device(t, "garage", 200*time.Millisecond))
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(c)

	for range 5 {
		if _, err := registry.Gather(); err != nil {
			t.Fatal(err)
		}
	}
	if got := ch.requests.Load(); got != 4 {
		t.Errorf("5 scrapes in one poll interval caused %d requests, want 4", got)
	}
	first := testutil.ToFloat64(scrapeMetric{c, "wall_connector_scrape_poll_timestamp_seconds"})

	time.Sleep(250 * time.Millisecond)
	if _, err := registry.Gather(); err != nil {
		t.Fatal(err)
	}
	if got := ch.requests.Load(); got != 8 {
		t.Errorf("a scrape after the poll interval: %d requests in total, want 8", got)
	}
	if second := testutil.ToFloat64(scrapeMetric{c, "wall_connector_scrape_poll_timestamp_seconds"}); second <= first {
		t.Errorf("the poll timestamp did not increase: %v, then %v", first, second)
	}
}

// scrapeMetric is a collector with only the series of one metric name, for
// testutil.ToFloat64.
type scrapeMetric struct {
	c    *Collector
	name string
}

func (s scrapeMetric) Describe(ch chan<- *prometheus.Desc) { s.c.Describe(ch) }

func (s scrapeMetric) Collect(ch chan<- prometheus.Metric) {
	all := make(chan prometheus.Metric)
	go func() {
		s.c.Collect(all)
		close(all)
	}()
	for m := range all {
		if strings.Contains(m.Desc().String(), `"`+s.name+`"`) {
			ch <- m
		}
	}
}

// A charger that cannot be reached: the exporter stays up, exports no old
// values, and sets wall_connector_scrape_collector_success to 0.
func TestChargerUnreachable(t *testing.T) {
	ch := newCharger(t)
	c := newCollector(t, ch.device(t, "garage", 100*time.Millisecond))

	text := exposition(t, c)
	if got := len(valueSeries(text)); got != 47 {
		t.Fatalf("the charger has %d value series before the failure, want 47", got)
	}

	ch.Close()
	time.Sleep(150 * time.Millisecond)
	text = exposition(t, c)
	wantSeries(t, text,
		`wall_connector_scrape_collector_success{collector="vitals",device="garage"} 0`,
		`wall_connector_scrape_collector_success{collector="lifetime",device="garage"} 0`,
		`wall_connector_scrape_collector_success{collector="wifi",device="garage"} 0`,
		`wall_connector_scrape_collector_success{collector="version",device="garage"} 0`,
	)
	if got := valueSeries(text); len(got) != 0 {
		t.Errorf("the exporter exports values of a charger that it cannot reach:\n%s", strings.Join(got, "\n"))
	}
	if !strings.Contains(text, `wall_connector_scrape_poll_timestamp_seconds{device="garage"}`) {
		t.Error("the poll timestamp is not in the output")
	}
}

// When the charger gives no response, the poll stops after the first
// request, and the failed poll is not repeated before the interval is over.
func TestNoResponseStopsThePoll(t *testing.T) {
	reader := &fakeReader{err: fmt.Errorf("%w: test", wallconnector.ErrNoResponse)}
	c := newCollector(t, Device{Name: "garage", Reader: reader, PollInterval: time.Minute})
	for range 3 {
		text := exposition(t, c)
		if got := valueSeries(text); len(got) != 0 {
			t.Errorf("values after a failed poll:\n%s", strings.Join(got, "\n"))
		}
		wantSeries(t, text, `wall_connector_scrape_collector_success{collector="version",device="garage"} 0`)
	}
	if reader.calls != 1 {
		t.Errorf("the exporter sent %d requests, want 1", reader.calls)
	}
	if reader.closed != 1 {
		t.Errorf("the exporter closed the connection %d times, want 1", reader.closed)
	}
}

// When one endpoint gives an error, only its collector fails.
func TestOneEndpointFails(t *testing.T) {
	ch := newCharger(t)
	ch.fail("/api/1/lifetime", http.StatusInternalServerError)
	text := exposition(t, newCollector(t, ch.device(t, "garage", time.Minute)))
	wantSeries(t, text,
		`wall_connector_scrape_collector_success{collector="vitals",device="garage"} 1`,
		`wall_connector_scrape_collector_success{collector="lifetime",device="garage"} 0`,
		`wall_connector_scrape_collector_success{collector="wifi",device="garage"} 1`,
		`wall_connector_scrape_collector_success{collector="version",device="garage"} 1`,
		`wall_connector_grid_voltage_volts{device="garage"} 231.4`,
		`wall_connector_wifi_rssi_dbm{device="garage"} -58`,
	)
	if strings.Contains(text, "wall_connector_lifetime_") {
		t.Error("the lifetime collector failed and must export no values")
	}
	if got := ch.requests.Load(); got != 4 {
		t.Errorf("the exporter sent %d requests, want 4", got)
	}
}

// One exporter reads more than one charger. One charger that fails does not
// change the values of the other.
func TestTwoChargers(t *testing.T) {
	garage, carport := newCharger(t), newCharger(t)
	carport.set("/api/1/vitals", "vitals_charging.json")
	c := newCollector(t, garage.device(t, "garage", 100*time.Millisecond), carport.device(t, "carport", 100*time.Millisecond))

	wantSeries(t, exposition(t, c),
		`wall_connector_evse_state{device="garage",state="not_connected"} 1`,
		`wall_connector_evse_state{device="carport",state="charging"} 1`,
	)

	garage.Close()
	time.Sleep(150 * time.Millisecond)
	text := exposition(t, c)
	wantSeries(t, text,
		`wall_connector_scrape_collector_success{collector="vitals",device="garage"} 0`,
		`wall_connector_scrape_collector_success{collector="vitals",device="carport"} 1`,
		`wall_connector_evse_state{device="carport",state="charging"} 1`,
	)
	if strings.Contains(text, `wall_connector_evse_state{device="garage"`) {
		t.Error("the exporter exports values of the charger that it cannot reach")
	}
}

func TestNewErrors(t *testing.T) {
	reader := &fakeReader{}
	logger := promslog.NewNopLogger()
	for name, devices := range map[string][]Device{
		"no device": nil,
		"no name":   {{Reader: reader}},
		"no reader": {{Name: "a"}},
		"same name": {{Name: "a", Reader: reader}, {Name: "a", Reader: reader}},
	} {
		if _, err := New(devices, logger); err == nil {
			t.Errorf("%s: New returned no error", name)
		}
	}
}

// The metric names follow the rules of Prometheus, with one exception: the
// energy is in watt hours and not in joules, as in modbus_exporter.
func TestLint(t *testing.T) {
	problems, err := testutil.CollectAndLint(newCollector(t, newCharger(t).device(t, "garage", time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range problems {
		if strings.Contains(problem.Metric, "_watt_hours") && strings.Contains(problem.Text, `instead of "hours"`) {
			continue
		}
		t.Errorf("%s: %s", problem.Metric, problem.Text)
	}
}

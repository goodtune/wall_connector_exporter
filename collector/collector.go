// Package collector turns the readings of Tesla Wall Connector chargers
// into Prometheus metrics. It keeps the last reading of each charger, so
// that a scrape does not always cause requests to the charger.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/goodtune/wall_connector_exporter/wallconnector"
)

const (
	namespace   = "wall_connector"
	deviceLabel = "device"
)

// The collectors. Each collector is one endpoint of the API.
const (
	collectorVitals = iota
	collectorLifetime
	collectorWifi
	collectorVersion
	numCollectors
)

var collectorNames = [numCollectors]string{"vitals", "lifetime", "wifi", "version"}

// StateUnknown is the value of the state label for a state code that has no
// reliable name.
const StateUnknown = "unknown"

// evseStates has the names of the values of evse_state. Tesla does not
// document the values. A value is in this table only when the Home Assistant
// integration, the ioBroker adapter and the Homey app give it the same
// meaning. The names are those of the Home Assistant integration.
//
// The values 3, 5, 7, 12 and 13 are not in the table: the sources do not
// know them or do not agree.
var evseStates = map[int]string{
	0:  "booting",
	1:  "not_connected",
	2:  "connected",
	4:  "ready",
	6:  "negotiating",
	8:  "charging_finished",
	9:  "waiting_car",
	10: "charging_reduced",
	11: "charging",
}

// StateName returns the name of a value of evse_state, or StateUnknown.
func StateName(code float64) string {
	if name, ok := evseStates[int(code)]; ok && code == float64(int(code)) {
		return name
	}
	return StateUnknown
}

// Reader reads one charger. The exporter has no other access to a charger.
type Reader interface {
	Vitals(ctx context.Context) (*wallconnector.Vitals, error)
	Lifetime(ctx context.Context) (*wallconnector.Lifetime, error)
	WifiStatus(ctx context.Context) (*wallconnector.WifiStatus, error)
	Version(ctx context.Context) (*wallconnector.Version, error)
	// Close closes the connection to the charger after a poll.
	Close()
}

// Device is one charger that the collector polls.
type Device struct {
	// Name is the value of the device label.
	Name   string
	Reader Reader
	// PollInterval is the shortest time between two polls of the charger. A
	// scrape that comes sooner gets the values of the last poll.
	PollInterval time.Duration
	// RequestPause is the pause between two requests of one poll.
	RequestPause time.Duration
	// SplitPhase selects the calculation of the charging power for a
	// split-phase supply.
	SplitPhase bool
}

// result is the outcome of the request of one collector.
type result struct {
	duration time.Duration
	err      error
}

// poll is the outcome of one poll of a charger. The data of a collector is
// nil when its request failed.
type poll struct {
	at       time.Time
	results  [numCollectors]result
	vitals   *wallconnector.Vitals
	lifetime *wallconnector.Lifetime
	wifi     *wallconnector.WifiStatus
	version  *wallconnector.Version
}

type device struct {
	Device

	mu   sync.Mutex
	last *poll
}

// Collector polls the chargers and makes their metrics.
type Collector struct {
	devices []*device
	logger  *slog.Logger
}

var descs []*prometheus.Desc

func desc(name, help string, labels ...string) *prometheus.Desc {
	d := prometheus.NewDesc(namespace+"_"+name, help, append([]string{deviceLabel}, labels...), nil)
	descs = append(descs, d)
	return d
}

var (
	scrapeDurationDesc = desc("scrape_collector_duration_seconds",
		"Time the request of the collector took in the last poll of the charger.", "collector")
	scrapeSuccessDesc = desc("scrape_collector_success",
		"Whether the collector got its data in the last poll: 1 for yes, 0 for no.", "collector")
	pollTimeDesc = desc("scrape_poll_timestamp_seconds",
		"Time of the last poll of the charger, in seconds since the Unix epoch.")

	// Collector "version".
	deviceInfoDesc = desc("device_info",
		"Part number and firmware version of the charger. The value is always 1.", "part_number", "firmware_version")

	// Collector "vitals".
	vehicleConnectedDesc = desc("vehicle_connected",
		"Whether a vehicle is connected: 1 for yes, 0 for no.")
	contactorClosedDesc = desc("contactor_closed",
		"Whether the contactor is closed, which connects the vehicle to the supply: 1 for yes, 0 for no.")
	evseStateCodeDesc = desc("evse_state_code",
		"State of the charger: the number that the charger gives as evse_state.")
	evseStateDesc = desc("evse_state",
		"State of the charger by name. The series of the current state has the value 1. The state is \""+StateUnknown+"\" for a number that has no reliable name.", "state")
	configStatusDesc = desc("config_status_code",
		"The number that the charger gives as config_status. The meaning of the values is not known.")
	notReadyReasonDesc = desc("evse_not_ready_reason",
		"A reason why the charger is not ready to charge. One series with the value 1 for each number in evse_not_ready_reasons. The meaning of the numbers is not known.", "reason")
	currentAlertsDesc = desc("current_alerts",
		"Number of alerts that are active now.")
	sessionEnergyDesc = desc("session_energy_watt_hours",
		"Energy that the charger supplied in the current charge session, in Wh.")
	sessionDurationDesc = desc("session_duration_seconds",
		"Duration of the current charge session in seconds.")
	vehicleCurrentDesc = desc("vehicle_current_amperes",
		"Current that the vehicle takes, in A.")
	phaseCurrentDesc = desc("phase_current_amperes",
		"Current of each conductor in A. The phase \"n\" is the neutral conductor.", "phase")
	phaseVoltageDesc = desc("phase_voltage_volts",
		"Voltage of each phase at the output of the charger, in V. It is near 0 while the contactor is open.", "phase")
	chargingPowerDesc = desc("charging_power_watts",
		"Estimate of the charging power in W. The charger does not measure power. The exporter calculates the sum of voltage times current of the phases, or, for a split-phase supply, grid voltage times vehicle current. The value is 0 while the contactor is open. The power factor is not known.")
	gridVoltageDesc = desc("grid_voltage_volts",
		"Voltage of the supply in V.")
	gridFrequencyDesc = desc("grid_frequency_hertz",
		"Frequency of the supply in Hz.")
	temperatureDesc = desc("temperature_celsius",
		"Temperature in °C of the charge handle, the circuit board (pcba) and the microcontroller (mcu).", "sensor")
	pilotVoltageDesc = desc("pilot_voltage_volts",
		"High level and low level of the control pilot signal in V.", "level")
	proximityVoltageDesc = desc("proximity_voltage_volts",
		"Voltage of the proximity signal in V.")
	relayVoltageDesc = desc("relay_coil_voltage_volts",
		"Voltage of the relay coils in V. Newer firmware gives the relays k1 and k2. Older firmware gives one value, with the relay \"coil\".", "relay")
	thermopileDesc = desc("input_thermopile_volts",
		"Voltage of the input thermopile in V.")
	uptimeDesc = desc("uptime_seconds",
		"Time since the charger started, in seconds.")

	// Collector "lifetime".
	lifetimeEnergyDesc = desc("lifetime_energy_watt_hours_total",
		"Energy that the charger supplied in its lifetime, in Wh.")
	lifetimeChargeStartsDesc = desc("lifetime_charge_starts_total",
		"Number of charge sessions that started.")
	lifetimeChargingTimeDesc = desc("lifetime_charging_time_seconds_total",
		"Time that the charger was charging, in seconds.")
	lifetimeUptimeDesc = desc("lifetime_uptime_seconds_total",
		"Time that the charger had power, in seconds.")
	lifetimeContactorCyclesDesc = desc("lifetime_contactor_cycles_total",
		"Number of times that the contactor closed.")
	lifetimeContactorCyclesLoadedDesc = desc("lifetime_contactor_cycles_loaded_total",
		"Number of times that the contactor operated with a load.")
	lifetimeConnectorCyclesDesc = desc("lifetime_connector_cycles_total",
		"Number of times that the connector was put into a vehicle.")
	lifetimeAlertsDesc = desc("lifetime_alerts_total",
		"Number of alerts. A large number is usual. It is not known what the charger counts.")
	lifetimeThermalFoldbacksDesc = desc("lifetime_thermal_foldbacks_total",
		"Number of times that the charger decreased the current because of its temperature.")
	lifetimeStartupTemperatureDesc = desc("lifetime_average_startup_temperature_celsius",
		"Average temperature at the start of the charger, in °C.")

	// Collector "wifi".
	wifiConnectedDesc = desc("wifi_connected",
		"Whether the charger is connected to a Wi-Fi network: 1 for yes, 0 for no.")
	wifiInternetDesc = desc("wifi_internet_connected",
		"Whether the charger has a connection to the internet: 1 for yes, 0 for no.")
	wifiSignalStrengthDesc = desc("wifi_signal_strength",
		"Quality of the Wi-Fi signal as the charger gives it. The number has no documented unit. A larger number is better.")
	wifiRSSIDesc = desc("wifi_rssi_dbm",
		"Received signal strength of the Wi-Fi signal in dBm.")
	wifiSNRDesc = desc("wifi_snr_decibels",
		"Signal-to-noise ratio of the Wi-Fi signal in dB.")
)

// New makes the collector for the chargers.
func New(devices []Device, logger *slog.Logger) (*Collector, error) {
	if len(devices) == 0 {
		return nil, errors.New("no device is given")
	}
	c := &Collector{logger: logger}
	names := map[string]bool{}
	for _, d := range devices {
		if d.Name == "" {
			return nil, errors.New("a device has no name")
		}
		if names[d.Name] {
			return nil, fmt.Errorf("device name %q is used two times", d.Name)
		}
		names[d.Name] = true
		if d.Reader == nil {
			return nil, fmt.Errorf("device %q: the device needs a reader", d.Name)
		}
		c.devices = append(c.devices, &device{Device: d})
	}
	return c, nil
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range descs {
		ch <- d
	}
}

// Collect implements prometheus.Collector. The chargers are polled at the
// same time.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, d := range c.devices {
		wg.Go(func() {
			c.collect(d, ch)
		})
	}
	wg.Wait()
}

// current returns the last poll of the charger. It polls the charger first
// when the last poll is older than the poll interval. Only one poll of a
// charger runs at a time, and a failed poll is also not repeated before the
// interval is over.
func (c *Collector) current(ctx context.Context, d *device) *poll {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.last != nil && time.Since(d.last.at) < d.PollInterval {
		return d.last
	}
	d.last = c.poll(ctx, d)
	return d.last
}

// poll reads the four endpoints of the charger, one after the other. When
// the charger gives no response to a request, the poll stops: the other
// requests are not sent. Thus a charger that is off costs one timeout and
// not four.
func (c *Collector) poll(ctx context.Context, d *device) *poll {
	defer d.Reader.Close()
	p := &poll{at: time.Now()}
	var stop error
	for i, name := range collectorNames {
		if stop != nil {
			p.results[i].err = stop
			continue
		}
		if i > 0 {
			if err := pause(ctx, d.RequestPause); err != nil {
				stop = err
				p.results[i].err = err
				continue
			}
		}
		begin := time.Now()
		var err error
		switch i {
		case collectorVitals:
			p.vitals, err = d.Reader.Vitals(ctx)
		case collectorLifetime:
			p.lifetime, err = d.Reader.Lifetime(ctx)
		case collectorWifi:
			p.wifi, err = d.Reader.WifiStatus(ctx)
		case collectorVersion:
			p.version, err = d.Reader.Version(ctx)
		}
		p.results[i] = result{duration: time.Since(begin), err: err}
		if err == nil {
			continue
		}
		c.logger.Error("request failed", "device", d.Name, "collector", name, "err", err)
		if errors.Is(err, wallconnector.ErrNoResponse) {
			stop = err
		}
	}
	// A collector that failed has no data.
	if p.results[collectorVitals].err != nil {
		p.vitals = nil
	}
	if p.results[collectorLifetime].err != nil {
		p.lifetime = nil
	}
	if p.results[collectorWifi].err != nil {
		p.wifi = nil
	}
	if p.results[collectorVersion].err != nil {
		p.version = nil
	}
	return p
}

// pause waits for the time d, or until ctx is done.
func pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// emitter sends the samples of one charger.
type emitter struct {
	ch     chan<- prometheus.Metric
	device string
}

func (e emitter) send(desc *prometheus.Desc, valueType prometheus.ValueType, value float64, labels ...string) {
	e.ch <- prometheus.MustNewConstMetric(desc, valueType, value, append([]string{e.device}, labels...)...)
}

// gauge sends a gauge sample, if the charger gave the value.
func (e emitter) gauge(desc *prometheus.Desc, value *float64, labels ...string) {
	if value != nil {
		e.send(desc, prometheus.GaugeValue, *value, labels...)
	}
}

// counter sends a counter sample, if the charger gave the value. A counter
// cannot be negative.
func (e emitter) counter(desc *prometheus.Desc, value *float64) {
	if value != nil && *value >= 0 {
		e.send(desc, prometheus.CounterValue, *value)
	}
}

// flag sends 1 for true and 0 for false, if the charger gave the value.
func (e emitter) flag(desc *prometheus.Desc, value *bool) {
	if value == nil {
		return
	}
	number := 0.0
	if *value {
		number = 1
	}
	e.send(desc, prometheus.GaugeValue, number)
}

func (c *Collector) collect(d *device, ch chan<- prometheus.Metric) {
	p := c.current(context.Background(), d)
	e := emitter{ch: ch, device: d.Name}
	e.send(pollTimeDesc, prometheus.GaugeValue, float64(p.at.UnixNano())/1e9)
	for i, name := range collectorNames {
		success := 1.0
		if p.results[i].err != nil {
			success = 0
		}
		e.send(scrapeDurationDesc, prometheus.GaugeValue, p.results[i].duration.Seconds(), name)
		e.send(scrapeSuccessDesc, prometheus.GaugeValue, success, name)
	}
	// Values are exported only for a collector that got its data in the
	// last poll. Old values are not exported.
	if v := p.vitals; v != nil {
		e.vitals(v, d.SplitPhase)
	}
	if l := p.lifetime; l != nil {
		e.lifetime(l)
	}
	if w := p.wifi; w != nil {
		e.flag(wifiConnectedDesc, w.Connected)
		e.flag(wifiInternetDesc, w.Internet)
		e.gauge(wifiSignalStrengthDesc, w.SignalStrength)
		e.gauge(wifiRSSIDesc, w.RSSI)
		e.gauge(wifiSNRDesc, w.SNR)
	}
	if v := p.version; v != nil && (v.PartNumber != nil || v.FirmwareVersion != nil) {
		e.send(deviceInfoDesc, prometheus.GaugeValue, 1, text(v.PartNumber), text(v.FirmwareVersion))
	}
}

func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (e emitter) vitals(v *wallconnector.Vitals, splitPhase bool) {
	e.flag(vehicleConnectedDesc, v.VehicleConnected)
	e.flag(contactorClosedDesc, v.ContactorClosed)
	if v.EVSEState != nil {
		e.gauge(evseStateCodeDesc, v.EVSEState)
		e.send(evseStateDesc, prometheus.GaugeValue, 1, StateName(*v.EVSEState))
	}
	e.gauge(configStatusDesc, v.ConfigStatus)
	// The same number two times must not make the same series two times.
	reasons := slices.Clone(v.EVSENotReadyReasons)
	slices.Sort(reasons)
	for _, reason := range slices.Compact(reasons) {
		e.send(notReadyReasonDesc, prometheus.GaugeValue, 1, strconv.FormatInt(reason, 10))
	}
	if v.CurrentAlerts != nil {
		e.send(currentAlertsDesc, prometheus.GaugeValue, float64(len(v.CurrentAlerts)))
	}
	e.gauge(sessionEnergyDesc, v.SessionEnergyWattHours)
	e.gauge(sessionDurationDesc, v.SessionSeconds)
	e.gauge(vehicleCurrentDesc, v.VehicleCurrentAmperes)
	e.gauge(phaseCurrentDesc, v.CurrentAAmperes, "a")
	e.gauge(phaseCurrentDesc, v.CurrentBAmperes, "b")
	e.gauge(phaseCurrentDesc, v.CurrentCAmperes, "c")
	e.gauge(phaseCurrentDesc, v.CurrentNAmperes, "n")
	e.gauge(phaseVoltageDesc, v.VoltageAVolts, "a")
	e.gauge(phaseVoltageDesc, v.VoltageBVolts, "b")
	e.gauge(phaseVoltageDesc, v.VoltageCVolts, "c")
	if power, ok := ChargingPower(v, splitPhase); ok {
		e.send(chargingPowerDesc, prometheus.GaugeValue, power)
	}
	e.gauge(gridVoltageDesc, v.GridVolts)
	e.gauge(gridFrequencyDesc, v.GridHertz)
	e.gauge(temperatureDesc, v.HandleCelsius, "handle")
	e.gauge(temperatureDesc, v.PCBACelsius, "pcba")
	e.gauge(temperatureDesc, v.MCUCelsius, "mcu")
	e.gauge(pilotVoltageDesc, v.PilotHighVolts, "high")
	e.gauge(pilotVoltageDesc, v.PilotLowVolts, "low")
	e.gauge(proximityVoltageDesc, v.ProximityVolts)
	e.gauge(relayVoltageDesc, v.RelayCoilVolts, "coil")
	e.gauge(relayVoltageDesc, v.RelayK1Volts, "k1")
	e.gauge(relayVoltageDesc, v.RelayK2Volts, "k2")
	if uv := v.InputThermopileMicrovolts; uv != nil {
		e.send(thermopileDesc, prometheus.GaugeValue, *uv/1e6)
	}
	e.gauge(uptimeDesc, v.UptimeSeconds)
}

func (e emitter) lifetime(l *wallconnector.Lifetime) {
	e.counter(lifetimeEnergyDesc, l.EnergyWattHours)
	e.counter(lifetimeChargeStartsDesc, l.ChargeStarts)
	e.counter(lifetimeChargingTimeDesc, l.ChargingSeconds)
	e.counter(lifetimeUptimeDesc, l.UptimeSeconds)
	e.counter(lifetimeContactorCyclesDesc, l.ContactorCycles)
	e.counter(lifetimeContactorCyclesLoadedDesc, l.ContactorCyclesLoaded)
	e.counter(lifetimeConnectorCyclesDesc, l.ConnectorCycles)
	e.counter(lifetimeAlertsDesc, l.AlertCount)
	e.counter(lifetimeThermalFoldbacksDesc, l.ThermalFoldbacks)
	e.gauge(lifetimeStartupTemperatureDesc, l.AvgStartupCelsius)
}

// ChargingPower returns an estimate of the charging power in W. The charger
// does not measure power.
//
//   - The usual calculation is the sum of voltage times current of the
//     phases a, b and c. It is for a three-phase supply and for a supply
//     with one phase.
//   - For a split-phase supply, the calculation is grid voltage times
//     vehicle current.
//   - While the contactor is open, the power is 0: the small currents that
//     the charger then measures are not a flow of energy to the vehicle.
//
// The result is volts times amperes. The power factor is not known. The
// second result is false when the charger did not give the necessary
// values.
func ChargingPower(v *wallconnector.Vitals, splitPhase bool) (float64, bool) {
	if v.ContactorClosed != nil && !*v.ContactorClosed {
		return 0, true
	}
	if splitPhase {
		if v.GridVolts == nil || v.VehicleCurrentAmperes == nil {
			return 0, false
		}
		return *v.GridVolts * *v.VehicleCurrentAmperes, true
	}
	power := 0.0
	for _, phase := range [][2]*float64{
		{v.VoltageAVolts, v.CurrentAAmperes},
		{v.VoltageBVolts, v.CurrentBAmperes},
		{v.VoltageCVolts, v.CurrentCAmperes},
	} {
		if phase[0] == nil || phase[1] == nil {
			return 0, false
		}
		power += *phase[0] * *phase[1]
	}
	return power, true
}

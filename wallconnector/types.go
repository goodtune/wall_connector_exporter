package wallconnector

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
)

// The field set of the API changes with the firmware version. Thus each
// field is a pointer: a field that the charger does not give stays nil, and
// the exporter then makes no sample for it. Fields that the types do not
// have are ignored.

// Vitals is the response of /api/1/vitals: the live values.
type Vitals struct {
	ContactorClosed  *bool `json:"contactor_closed"`
	VehicleConnected *bool `json:"vehicle_connected"`

	SessionSeconds         *float64 `json:"session_s"`
	SessionEnergyWattHours *float64 `json:"session_energy_wh"`

	GridVolts             *float64 `json:"grid_v"`
	GridHertz             *float64 `json:"grid_hz"`
	VehicleCurrentAmperes *float64 `json:"vehicle_current_a"`

	CurrentAAmperes *float64 `json:"currentA_a"`
	CurrentBAmperes *float64 `json:"currentB_a"`
	CurrentCAmperes *float64 `json:"currentC_a"`
	CurrentNAmperes *float64 `json:"currentN_a"`
	VoltageAVolts   *float64 `json:"voltageA_v"`
	VoltageBVolts   *float64 `json:"voltageB_v"`
	VoltageCVolts   *float64 `json:"voltageC_v"`

	// Older firmware gives relay_coil_v. Newer firmware gives relay_k1_v
	// and relay_k2_v.
	RelayCoilVolts *float64 `json:"relay_coil_v"`
	RelayK1Volts   *float64 `json:"relay_k1_v"`
	RelayK2Volts   *float64 `json:"relay_k2_v"`

	PCBACelsius   *float64 `json:"pcba_temp_c"`
	HandleCelsius *float64 `json:"handle_temp_c"`
	MCUCelsius    *float64 `json:"mcu_temp_c"`

	UptimeSeconds             *float64 `json:"uptime_s"`
	InputThermopileMicrovolts *float64 `json:"input_thermopile_uv"`
	ProximityVolts            *float64 `json:"prox_v"`
	PilotHighVolts            *float64 `json:"pilot_high_v"`
	PilotLowVolts             *float64 `json:"pilot_low_v"`

	ConfigStatus *float64 `json:"config_status"`
	EVSEState    *float64 `json:"evse_state"`
	// CurrentAlerts has one element for each active alert. The type of the
	// elements is not known, thus they are not decoded.
	CurrentAlerts []json.RawMessage `json:"current_alerts"`
	// EVSENotReadyReasons has the codes of the reasons why the charger is
	// not ready to charge.
	EVSENotReadyReasons []int64 `json:"evse_not_ready_reasons"`
}

// Lifetime is the response of /api/1/lifetime: the counters since the
// charger was made.
type Lifetime struct {
	ContactorCycles       *float64 `json:"contactor_cycles"`
	ContactorCyclesLoaded *float64 `json:"contactor_cycles_loaded"`
	AlertCount            *float64 `json:"alert_count"`
	ThermalFoldbacks      *float64 `json:"thermal_foldbacks"`
	AvgStartupCelsius     *float64 `json:"avg_startup_temp"`
	ChargeStarts          *float64 `json:"charge_starts"`
	EnergyWattHours       *float64 `json:"energy_wh"`
	ConnectorCycles       *float64 `json:"connector_cycles"`
	UptimeSeconds         *float64 `json:"uptime_s"`
	ChargingSeconds       *float64 `json:"charging_time_s"`
}

// WifiStatus is the response of /api/1/wifi_status. The response also has
// the name of the network and addresses of the charger. The type has no
// fields for them, so that the exporter cannot export them.
type WifiStatus struct {
	SignalStrength *float64 `json:"wifi_signal_strength"`
	RSSI           *float64 `json:"wifi_rssi"`
	SNR            *float64 `json:"wifi_snr"`
	Connected      *bool    `json:"wifi_connected"`
	Internet       *bool    `json:"internet"`
}

// Version is the response of /api/1/version. The response also identifies
// the individual charger. The type has no field for that, so that the
// exporter cannot export it.
type Version struct {
	FirmwareVersion *string `json:"firmware_version"`
	PartNumber      *string `json:"part_number"`
}

// decode reads a JSON object into the struct that v points to. Each field
// of the object is decoded separately:
//
//   - A field that the struct does not have is ignored.
//   - A field that the object does not have, or that is null, stays nil.
//   - A field whose value has a type that the struct does not expect stays
//     nil. The other fields are still decoded.
func decode(data []byte, v any) error {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return errors.New("the response is not a JSON object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	target := reflect.ValueOf(v).Elem()
	for i := range target.NumField() {
		raw, ok := object[target.Type().Field(i).Tag.Get("json")]
		if !ok || string(raw) == "null" {
			continue
		}
		field := reflect.New(target.Field(i).Type())
		if err := json.Unmarshal(raw, field.Interface()); err != nil {
			continue
		}
		target.Field(i).Set(field.Elem())
	}
	return nil
}

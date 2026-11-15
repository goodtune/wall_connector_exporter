# wall_connector_exporter

A Prometheus exporter for the Tesla Wall Connector (Gen 3).
The exporter reads the local HTTP API of each charger and exports the values on `/metrics`.

The charger has the API on its Wi-Fi address. The API needs no account and no cloud service.
Tesla does not document the API. See [States and codes](#states-and-codes) for what is known about its values.

The structure of the code and the flags are the same as in `node_exporter`, [`modbus_exporter`](https://github.com/goodtune/modbus_exporter) and [`amber_exporter`](https://github.com/goodtune/amber_exporter).

## Safety

The exporter only reads.

- It sends HTTP `GET` requests to four paths, and no other requests: `/api/1/vitals`, `/api/1/lifetime`, `/api/1/wifi_status` and `/api/1/version`.
- The code has no write path. The API has no known write endpoint. A test fails if a Go file names an HTTP method other than `GET`.
- It sends one request at a time to a charger, with a pause between two requests.
- It polls a charger not more than one time in 10 seconds. A poll interval of less than 5 seconds is not accepted.
- It does not follow a redirect, and it does not use a proxy.

## Quick start

1. Get the exporter from the [releases page](https://github.com/goodtune/wall_connector_exporter/releases), or build it with `make build`.
2. Start the exporter with the address of the charger:

   ```sh
   ./wall_connector_exporter --wall-connector.address=192.0.2.10 --wall-connector.device-name=garage
   ```

3. Read the metrics at <http://localhost:10064/metrics>.

To poll more than one charger, use a [configuration file](#configuration-file).

The directory `examples` has a configuration file and a Prometheus configuration.

## Install

Each release has these artifacts:

- Archives of the binary for Linux and macOS, for `amd64` and `arm64`.
- Packages for Linux: `rpm`, `deb`, and `apk`.
- A container image: `ghcr.io/goodtune/wall_connector_exporter`.

### Linux package

The package installs the binary, a systemd unit, and the files `/etc/wall_connector_exporter/config.yml` and `/etc/wall_connector_exporter/environment`.
The unit is not enabled after the installation, because the exporter cannot start without a charger.

1. Install the package.
2. Put the chargers in `/etc/wall_connector_exporter/config.yml`.
3. To set flags, put them in `/etc/wall_connector_exporter/environment`, on the line `WALL_CONNECTOR_EXPORTER_ARGS=`.
4. Start the service:

   ```sh
   sudo systemctl enable --now wall_connector_exporter.service
   ```

If the configuration is not correct, the exporter stops with exit status 2, and systemd does not start it again.

### Container

```sh
docker run -p 10064:10064 ghcr.io/goodtune/wall_connector_exporter \
  --wall-connector.address=192.0.2.10
```

Note: Port 10064 is not registered in the Prometheus list of default port allocations.

## Flags

| Flag | Default | Function |
|---|---|---|
| `--web.listen-address` | `:10064` | Address for the web server. You can repeat the flag. |
| `--web.config.file` | none | File for TLS and basic authentication, in the `exporter-toolkit` format. |
| `--web.telemetry-path` | `/metrics` | Path of the metrics. |
| `--web.disable-exporter-metrics` | off | Remove the `go_*`, `process_*`, and `promhttp_*` metrics. |
| `--config.file` | none | Configuration file that lists the chargers. |
| `--wall-connector.address` | none | Host name or IP address of one charger. Use it when there is no configuration file. |
| `--wall-connector.device-name` | `wall_connector` | Value of the `device` label for the charger of `--wall-connector.address`. |
| `--wall-connector.split-phase` | off | The charger of `--wall-connector.address` has a split-phase supply. See [Charging power](#charging-power). |
| `--wall-connector.poll-interval` | `10s` | Shortest time between two polls of a charger. Not less than `5s`. |
| `--wall-connector.timeout` | `5s` | Timeout of one HTTP request. |
| `--wall-connector.request-pause` | `100ms` | Pause between two requests of one poll. |
| `--log.level`, `--log.format` | `info`, `logfmt` | Log settings. |

Give `--config.file` or `--wall-connector.address`. The exporter does not accept the two together.

With a configuration file, `--wall-connector.poll-interval`, `--wall-connector.timeout` and `--wall-connector.request-pause` are the defaults for the chargers that do not set their own value.

## Configuration file

The configuration file lists the chargers. One exporter can poll many chargers, and each charger has its own poll.

```yaml
devices:
  - name: garage
    address: 192.0.2.10

  - name: carport
    address: 192.0.2.11:80
    poll_interval: 30s
    timeout: 3s
    request_pause: 200ms
    split_phase: true
```

| Key | Default | Function |
|---|---|---|
| `name` | `wall_connector` | Value of the `device` label of all metrics of the charger. Each charger must have a different name. |
| `address` | none, necessary | Host name or IP address of the charger, or `host:port`. The default port is 80. Not a URL. |
| `poll_interval` | `--wall-connector.poll-interval` | Shortest time between two polls. Not less than `5s`. |
| `timeout` | `--wall-connector.timeout` | Timeout of one HTTP request. |
| `request_pause` | `--wall-connector.request-pause` | Pause between two requests of one poll. |
| `split_phase` | `false` | The charger has a split-phase supply. See [Charging power](#charging-power). |

A key that the format does not have is an error. Two chargers with the same name or the same address are an error.

## How the exporter polls

Prometheus can scrape more frequently than is good for a charger.
Thus the exporter keeps the values of the last poll of each charger.
When the last poll is older than the poll interval, the subsequent scrape starts a new poll.
A scrape that comes sooner gets the values of the last poll.

One poll is four requests, one for each collector: `vitals`, `lifetime`, `wifi` and `version`.

- If the charger answers a request with an error or with data that is not JSON, only that collector fails.
- If the charger gives no response, the poll stops and all collectors that are not read fail. A charger that is off thus costs one timeout and not four.
- A collector that failed exports no values and sets `wall_connector_scrape_collector_success` to 0. The exporter does not export old values.
- After a failed poll, the exporter waits for the poll interval before it tries again.
- The exporter does not stop when it cannot reach a charger.

The fields of the API change with the firmware version.
The exporter makes no sample for a field that the charger does not give, or that has a type that the exporter does not expect.
It ignores fields that it does not know.

## Metrics

All metric names start with `wall_connector_`. Each metric has the label `device`, which is the name that you give the charger.
Values are in base units: `volts`, `amperes`, `watts`, `hertz`, `celsius` and `seconds`.
Energy is in `watt_hours`, because that is the unit of the charger. This is the same decision as in `modbus_exporter`.

The cardinality is low and fixed: one charger has approximately 50 series.
The labels with a variable value are `state` (one series, for the current state), `reason` (one series for each active reason code) and `firmware_version` (a new series only after a firmware update).

### Collector `vitals`

From `/api/1/vitals`. "Field" is the name in the API.

| Metric | Labels | Unit | Field |
|---|---|---|---|
| `wall_connector_vehicle_connected` | | 0 or 1 | `vehicle_connected` |
| `wall_connector_contactor_closed` | | 0 or 1 | `contactor_closed` |
| `wall_connector_evse_state_code` | | code | `evse_state` |
| `wall_connector_evse_state` | `state` | always 1 | `evse_state`, by name |
| `wall_connector_config_status_code` | | code | `config_status` |
| `wall_connector_evse_not_ready_reason` | `reason` | always 1 | One series for each number in `evse_not_ready_reasons` |
| `wall_connector_current_alerts` | | count | Number of elements of `current_alerts` |
| `wall_connector_session_energy_watt_hours` | | Wh | `session_energy_wh` |
| `wall_connector_session_duration_seconds` | | s | `session_s` |
| `wall_connector_vehicle_current_amperes` | | A | `vehicle_current_a` |
| `wall_connector_phase_current_amperes` | `phase`: `a`, `b`, `c`, `n` | A | `currentA_a`, `currentB_a`, `currentC_a`, `currentN_a` |
| `wall_connector_phase_voltage_volts` | `phase`: `a`, `b`, `c` | V | `voltageA_v`, `voltageB_v`, `voltageC_v` |
| `wall_connector_charging_power_watts` | | W | Calculated. See [Charging power](#charging-power). |
| `wall_connector_grid_voltage_volts` | | V | `grid_v` |
| `wall_connector_grid_frequency_hertz` | | Hz | `grid_hz` |
| `wall_connector_temperature_celsius` | `sensor`: `handle`, `pcba`, `mcu` | °C | `handle_temp_c`, `pcba_temp_c`, `mcu_temp_c` |
| `wall_connector_pilot_voltage_volts` | `level`: `high`, `low` | V | `pilot_high_v`, `pilot_low_v` |
| `wall_connector_proximity_voltage_volts` | | V | `prox_v` |
| `wall_connector_relay_coil_voltage_volts` | `relay`: `k1`, `k2`, or `coil` | V | `relay_k1_v`, `relay_k2_v`, or `relay_coil_v` of older firmware |
| `wall_connector_input_thermopile_volts` | | V | `input_thermopile_uv`, divided by 1 000 000 |
| `wall_connector_uptime_seconds` | | s | `uptime_s`: the time since the charger started |

The phase `n` is the neutral conductor. The sensor `pcba` is the circuit board, and `mcu` is the microcontroller.

The session metrics are gauges: they go back to 0 for a new session.

### Collector `lifetime`

From `/api/1/lifetime`. These are counters. They do not go back to 0 when the charger starts again.

| Metric | Unit | Field |
|---|---|---|
| `wall_connector_lifetime_energy_watt_hours_total` | Wh | `energy_wh` |
| `wall_connector_lifetime_charge_starts_total` | count | `charge_starts` |
| `wall_connector_lifetime_charging_time_seconds_total` | s | `charging_time_s` |
| `wall_connector_lifetime_uptime_seconds_total` | s | `uptime_s` |
| `wall_connector_lifetime_contactor_cycles_total` | count | `contactor_cycles` |
| `wall_connector_lifetime_contactor_cycles_loaded_total` | count | `contactor_cycles_loaded` |
| `wall_connector_lifetime_connector_cycles_total` | count | `connector_cycles` |
| `wall_connector_lifetime_alerts_total` | count | `alert_count` |
| `wall_connector_lifetime_thermal_foldbacks_total` | count | `thermal_foldbacks` |
| `wall_connector_lifetime_average_startup_temperature_celsius` | °C, gauge | `avg_startup_temp` |

A large value of `wall_connector_lifetime_alerts_total` is usual. It is not known what the charger counts.

### Collector `wifi`

From `/api/1/wifi_status`.

| Metric | Unit | Field |
|---|---|---|
| `wall_connector_wifi_connected` | 0 or 1 | `wifi_connected` |
| `wall_connector_wifi_internet_connected` | 0 or 1 | `internet` |
| `wall_connector_wifi_rssi_dbm` | dBm | `wifi_rssi` |
| `wall_connector_wifi_snr_decibels` | dB | `wifi_snr` |
| `wall_connector_wifi_signal_strength` | none | `wifi_signal_strength` |

`wifi_signal_strength` is a quality number without a documented unit. A larger number is better. The exporter gives the number of the charger and does not change it.

### Collector `version`

From `/api/1/version`.

| Metric | Labels | Description |
|---|---|---|
| `wall_connector_device_info` | `part_number`, `firmware_version` | The charger. The value is 1. |

### Exporter metrics

| Metric | Labels | Description |
|---|---|---|
| `wall_connector_scrape_collector_success` | `device`, `collector` | 1 when the collector got its data in the last poll. |
| `wall_connector_scrape_collector_duration_seconds` | `device`, `collector` | Time of the request of the collector in the last poll. |
| `wall_connector_scrape_poll_timestamp_seconds` | `device` | Time of the last poll of the charger. |
| `wall_connector_exporter_build_info` | `version`, `revision`, `branch`, `goversion`, `goos`, `goarch`, `tags` | The build of the exporter. The value is 1. |

Example series, with values that are not from a real charger:

```
wall_connector_device_info{device="garage",part_number="0000000-00-A",firmware_version="1.2.3+g0123456789abcd"} 1
wall_connector_vehicle_connected{device="garage"} 1
wall_connector_evse_state{device="garage",state="charging"} 1
wall_connector_charging_power_watts{device="garage"} 11038
wall_connector_phase_voltage_volts{device="garage",phase="a"} 230
wall_connector_temperature_celsius{device="garage",sensor="handle"} 30.5
wall_connector_lifetime_energy_watt_hours_total{device="garage"} 1.234567e+06
wall_connector_scrape_collector_success{device="garage",collector="vitals"} 1
```

### Values that the exporter does not export

The API gives values that identify one charger or its network.
The exporter does not read them into its data, and thus cannot export or log them:

- the serial number of the charger (`serial_number`)
- the name of the Wi-Fi network (`wifi_ssid`)
- the hardware address and the IP address of the charger (`wifi_mac`, `wifi_infra_ip`)

It also does not export `git_branch`, `web_service` and the `IEEE1547…Crc` fields of `/api/1/version`.

The address of the charger is not in a label. It is in the log of the exporter.
Prometheus adds the `instance` and `job` labels of the exporter.

## Charging power

The charger does not measure power. `wall_connector_charging_power_watts` is an estimate that the exporter calculates:

- **Usual calculation:** voltage times current of each of the phases `a`, `b` and `c`, and the sum of the three products. Use it for a three-phase supply and for a supply with one phase.
- **With `split_phase`:** grid voltage times vehicle current (`grid_v` × `vehicle_current_a`). Use it for a split-phase supply, where the usual calculation gives a wrong value.
- **Contactor open:** the value is 0. The charger measures small currents when it does not charge. These are not a flow of energy to the vehicle.

The two calculations are those of the Home Assistant integration and the ioBroker adapter. The rule for the open contactor is a decision of this exporter.

The value is volts times amperes. The power factor is not known, thus the real power can be a little less.
For energy, use `wall_connector_lifetime_energy_watt_hours_total`, which the charger counts.

## States and codes

Tesla does not document the values of the API. The names in this exporter come from these sources:

- **HA**: the Home Assistant integration [`tesla_wall_connector`](https://github.com/home-assistant/core/tree/dev/homeassistant/components/tesla_wall_connector), table `EVSE_STATE` in `sensor.py`, and its Python library [`einarhauks/tesla-wall-connector`](https://github.com/einarhauks/tesla-wall-connector).
- **ioBroker**: the adapter [`nobl/ioBroker.tesla-wallconnector3`](https://github.com/nobl/ioBroker.tesla-wallconnector3), `docs/en/README.md`.
- **Homey**: the app [`vaernes/homey_tesla_power_connect`](https://github.com/vaernes/homey_tesla_power_connect), `lib/evsestate.ts`.
- **wallconnectorlog**: [`mews-se/wallconnectorlog`](https://github.com/mews-se/wallconnectorlog), `README.md`.
- Other Prometheus exporters, for example [`benclapp/tesla_wall_connector_exporter`](https://github.com/benclapp/tesla_wall_connector_exporter), export the codes as numbers only.

### `evse_state`

`wall_connector_evse_state_code` is the number. `wall_connector_evse_state` has the name in the label `state`.
A number has a name only when HA, ioBroker and Homey give it the same meaning. The names are those of HA.

| Code | `state` | Meaning in the sources |
|---|---|---|
| 0 | `booting` | The charger starts. |
| 1 | `not_connected` | No vehicle is connected. |
| 2 | `connected` | A vehicle is connected. The charger is not ready. |
| 4 | `ready` | A vehicle is connected. The charger is ready to charge. |
| 6 | `negotiating` | A vehicle is connected. The charger and the vehicle negotiate. |
| 8 | `charging_finished` | A vehicle is connected. The charge is complete or was stopped. |
| 9 | `waiting_car` | The charger is ready and waits for the vehicle. |
| 10 | `charging_reduced` | The charger charges with decreased power. |
| 11 | `charging` | The charger charges. |
| 3, 5, 12, 13 | `unknown` | No source knows the meaning. |
| 7 | `unknown` | The sources do not agree: HA says "error", Homey says "finished charging", wallconnectorlog says that it is a short state after the connection of a vehicle, and ioBroker says that it is not documented. |
| all others | `unknown` | |

Use the code, and not the name, for a state that is `unknown`.

### Codes without a name

No source gives a meaning to the values of these fields. wallconnectorlog says that no published table agrees with what chargers give.
The exporter exports the numbers and gives them no names.

- **`config_status`**: `wall_connector_config_status_code`.
- **`evse_not_ready_reasons`**: a list of numbers. `wall_connector_evse_not_ready_reason{reason="4"}` is 1 while the number 4 is in the list. A number that is not in the list has no series.
- **`current_alerts`**: a list. `wall_connector_current_alerts` is the number of elements. The exporter does not export the elements, because their type and their set of values are not known.

## Not verified

The exporter was tested against one charger with firmware `26.34.0`, with no vehicle connected. The test found these facts:

- The API is plain HTTP on port 80, without a login. The four paths answer with JSON.
- The newer field names `relay_k1_v` and `relay_k2_v` are there, and `relay_coil_v` is not.
- `/api/1/wifi_status` gives the name of the network in base64.
- One poll of four requests takes approximately 0.7 s. Of that time, 0.3 s is the pause between requests.
- The state was `not_connected` (code 1), and the list of reasons was not empty.

These items were not seen on a charger. They come from the sources and from the tests of this repository only:

- **A charge session.** All states other than `not_connected`, the session energy and duration, a closed contactor, and currents and voltages under load.
- **The charging power.** The calculation was not compared with a meter. It is not known which of the two calculations is correct for each type of supply.
- **Alerts.** The list `current_alerts` was always empty.
- **The reason codes.** Their meaning is not known. The test saw the same codes during the full test.
- **A charger that starts again.** It is not known whether a lifetime counter can decrease.
- **Older firmware.** The field `relay_coil_v` is in the tests only.
- **The handle temperature.** wallconnectorlog says that `handle_temp_c` is 255 while the button of the handle is pushed. The exporter exports the value that the charger gives.
- **Long operation.** The test ran for less than one minute.

## Queries

Charging power of all chargers:

```promql
sum(wall_connector_charging_power_watts)
```

Energy that a charger supplied in the last 24 hours, in kWh:

```promql
increase(wall_connector_lifetime_energy_watt_hours_total[24h]) / 1000
```

Chargers that charge now:

```promql
wall_connector_evse_state{state=~"charging|charging_reduced"}
```

Chargers that the exporter cannot read:

```promql
wall_connector_scrape_collector_success{collector="vitals"} == 0
```

Firmware version of each charger:

```promql
wall_connector_device_info
```

## Development

```sh
make all            # format check, vet, tests, build
make lint           # golangci-lint
make release-check  # validate .goreleaser.yml
make snapshot       # build all release artifacts into dist/, publish nothing
```

The tests do not use the network and do not need a charger.
They use an HTTP server that is part of the tests, with values that are not from a real charger.

The packages:

| Package | Function |
|---|---|
| `wallconnector` | Read-only HTTP client for the API, and the decoding of the responses. |
| `config` | The configuration file. |
| `collector` | Polls the chargers and makes their metrics. |

## Release

[GoReleaser](https://goreleaser.com) builds and publishes the releases.

1. Publish a GitHub release with a tag in the format `v1.2.3`.
2. The workflow `Release` then runs GoReleaser.

GoReleaser adds the archives, the packages, and the checksums to the GitHub release.
It also pushes the container image to `ghcr.io/goodtune/wall_connector_exporter` with the tag of the release and the tag `latest`.

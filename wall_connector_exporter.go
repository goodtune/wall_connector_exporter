// Command wall_connector_exporter exports the values of the local HTTP API
// of Tesla Wall Connector (Gen 3) chargers as Prometheus metrics. The
// exporter only reads: it sends GET requests and no other requests.
package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	"github.com/prometheus/exporter-toolkit/web/kingpinflag"

	"github.com/goodtune/wall_connector_exporter/collector"
	"github.com/goodtune/wall_connector_exporter/config"
	"github.com/goodtune/wall_connector_exporter/wallconnector"
)

const exporterName = "wall_connector_exporter"

// exitConfig is the exit status for a configuration that is not correct.
// The systemd unit does not start the exporter again after this status.
const exitConfig = 2

func main() {
	setDefaultVersion()
	os.Exit(run())
}

// setDefaultVersion gives the build a version when linker flags did not set
// one, as with a plain "go build".
func setDefaultVersion() {
	if version.Version != "" {
		return
	}
	version.Version = "unknown"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		version.Version = info.Main.Version
	}
}

// settings are the flags that select the chargers.
type settings struct {
	configFile string
	address    string
	deviceName string
	splitPhase bool
	defaults   config.Defaults
}

// devices returns the chargers of the configuration file, or the one
// charger of the --wall-connector.* flags.
func (s settings) devices() ([]config.Device, error) {
	if s.configFile != "" {
		if s.address != "" {
			return nil, errors.New("use --config.file or --wall-connector.address, and not the two")
		}
		c, err := config.Load(s.configFile, s.defaults)
		if err != nil {
			return nil, err
		}
		return c.Devices, nil
	}
	if s.address == "" {
		return nil, errors.New("give --config.file or --wall-connector.address")
	}
	c := &config.Config{Devices: []config.Device{{
		Name: s.deviceName, Address: s.address, SplitPhase: s.splitPhase,
	}}}
	if err := c.Validate(s.defaults); err != nil {
		return nil, err
	}
	return c.Devices, nil
}

// newCollector makes the client of each charger and the collector.
func newCollector(devices []config.Device, logger *slog.Logger) (*collector.Collector, error) {
	var targets []collector.Device
	for _, d := range devices {
		client, err := wallconnector.NewClient(d.Address, d.Timeout)
		if err != nil {
			return nil, err
		}
		logger.Info("Device", "device", d.Name, "address", d.Address, "poll_interval", d.PollInterval,
			"timeout", d.Timeout, "split_phase", d.SplitPhase)
		targets = append(targets, collector.Device{
			Name:         d.Name,
			Reader:       client,
			PollInterval: d.PollInterval,
			RequestPause: d.RequestPause,
			SplitPhase:   d.SplitPhase,
		})
	}
	return collector.New(targets, logger)
}

func run() int {
	app := kingpin.New(exporterName, "Prometheus exporter for the local API of the Tesla Wall Connector (Gen 3). The exporter only reads, it has no write access to a charger.")
	var s settings
	var (
		metricsPath = app.Flag("web.telemetry-path", "Path under which to expose metrics.").
				Default("/metrics").String()
		disableExporterMetrics = app.Flag("web.disable-exporter-metrics", "Exclude metrics about the exporter itself (promhttp_*, process_*, go_*).").
					Bool()
		toolkitFlags = kingpinflag.AddFlags(app, ":10064")
	)
	app.Flag("config.file", "Configuration file that lists the chargers. Use it to poll more than one charger.").
		StringVar(&s.configFile)
	app.Flag("wall-connector.address", "Host name or IP address of one charger. The API is on port 80. Give host:port for a different port.").
		StringVar(&s.address)
	app.Flag("wall-connector.device-name", "Value of the device label for the charger of --wall-connector.address.").
		Default(config.DefaultName).StringVar(&s.deviceName)
	app.Flag("wall-connector.split-phase", "The charger of --wall-connector.address has a split-phase supply. The charging power is then grid voltage times vehicle current.").
		BoolVar(&s.splitPhase)
	app.Flag("wall-connector.poll-interval", "Shortest time between two polls of a charger, "+config.MinPollInterval.String()+" or more. A charger in the configuration file can set its own value.").
		Default("10s").DurationVar(&s.defaults.PollInterval)
	app.Flag("wall-connector.timeout", "Timeout of one HTTP request. A charger in the configuration file can set its own value.").
		Default("5s").DurationVar(&s.defaults.Timeout)
	app.Flag("wall-connector.request-pause", "Pause between two HTTP requests of one poll. A charger in the configuration file can set its own value.").
		Default("100ms").DurationVar(&s.defaults.RequestPause)

	promslogConfig := &promslog.Config{}
	flag.AddFlags(app, promslogConfig)
	app.Version(version.Print(exporterName))
	app.HelpFlag.Short('h')
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := promslog.New(promslogConfig)

	logger.Info("Starting "+exporterName, "version", version.Info())
	logger.Info("Build context", "build_context", version.BuildContext())

	devices, err := s.devices()
	if err != nil {
		logger.Error("The configuration is not correct", "err", err)
		return exitConfig
	}
	chargerCollector, err := newCollector(devices, logger)
	if err != nil {
		logger.Error("The configuration is not correct", "err", err)
		return exitConfig
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(chargerCollector, versioncollector.NewCollector(exporterName))
	handlerOpts := promhttp.HandlerOpts{
		ErrorLog:      slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ErrorHandling: promhttp.ContinueOnError,
	}
	var metricsHandler http.Handler
	if *disableExporterMetrics {
		metricsHandler = promhttp.HandlerFor(registry, handlerOpts)
	} else {
		registry.MustRegister(
			collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
			collectors.NewGoCollector(),
		)
		handlerOpts.Registry = registry
		metricsHandler = promhttp.InstrumentMetricHandler(registry, promhttp.HandlerFor(registry, handlerOpts))
	}

	mux := http.NewServeMux()
	mux.Handle(*metricsPath, metricsHandler)
	if *metricsPath != "/" {
		landingPage, err := web.NewLandingPage(web.LandingConfig{
			Name:        "Wall Connector Exporter",
			Description: "Prometheus exporter for the Tesla Wall Connector",
			Version:     version.Info(),
			Links:       []web.LandingLinks{{Address: *metricsPath, Text: "Metrics"}},
		})
		if err != nil {
			logger.Error("Cannot create the landing page", "err", err)
			return 1
		}
		mux.Handle("/", landingPage)
	}

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := web.ListenAndServe(server, toolkitFlags, logger); err != nil {
		logger.Error("The web server stopped", "err", err)
		return 1
	}
	return 0
}

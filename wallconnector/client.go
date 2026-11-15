// Package wallconnector reads the local HTTP API of a Tesla Wall Connector
// (Gen 3). The client only sends GET requests to the four endpoints of the
// API. It has no other access to a charger.
package wallconnector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The endpoints of the API.
const (
	pathVitals     = "/api/1/vitals"
	pathLifetime   = "/api/1/lifetime"
	pathWifiStatus = "/api/1/wifi_status"
	pathVersion    = "/api/1/version"
)

// defaultPort is the port of the API.
const defaultPort = "80"

// maxResponseBytes is the limit for the size of a response. The responses
// of a charger are less than 1 kB.
const maxResponseBytes = 1 << 20

// ErrNoResponse says that the charger gave no HTTP response: no connection,
// a timeout, or a connection that broke.
var ErrNoResponse = errors.New("no response from the charger")

// HostPort returns address as host:port. An address without a port gets the
// port of the API. The address is a host name or an IP address, and not a
// URL.
func HostPort(address string) (string, error) {
	if address == "" {
		return "", errors.New("no address is given")
	}
	if strings.ContainsAny(address, "/?#@ ") {
		return "", fmt.Errorf("address %q must be a host or host:port, and not a URL", address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host, port = strings.TrimSuffix(strings.TrimPrefix(address, "["), "]"), defaultPort
	}
	if host == "" || port == "" {
		return "", fmt.Errorf("address %q must be a host or host:port", address)
	}
	hostPort := net.JoinHostPort(host, port)
	if u, err := url.Parse("http://" + hostPort); err != nil || u.Host != hostPort {
		return "", fmt.Errorf("address %q must be a host or host:port", address)
	}
	return hostPort, nil
}

// Client reads one charger.
type Client struct {
	base string
	http *http.Client
}

// NewClient makes the client for the charger at address, which is a host or
// host:port. The timeout is for one request.
func NewClient(address string, timeout time.Duration) (*Client, error) {
	hostPort, err := HostPort(address)
	if err != nil {
		return nil, err
	}
	return &Client{
		base: "http://" + hostPort,
		http: &http.Client{
			Timeout: timeout,
			// A charger is on the local network: no proxy. One connection
			// is sufficient, because the requests go one after the other.
			Transport: &http.Transport{
				Proxy:           nil,
				MaxIdleConns:    1,
				MaxConnsPerHost: 1,
				IdleConnTimeout: 30 * time.Second,
			},
			// The API does not redirect. The client does not go to
			// another address.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Vitals reads the live values.
func (c *Client) Vitals(ctx context.Context) (*Vitals, error) {
	v := &Vitals{}
	return v, c.get(ctx, pathVitals, v)
}

// Lifetime reads the lifetime counters.
func (c *Client) Lifetime(ctx context.Context) (*Lifetime, error) {
	v := &Lifetime{}
	return v, c.get(ctx, pathLifetime, v)
}

// WifiStatus reads the state of the Wi-Fi connection.
func (c *Client) WifiStatus(ctx context.Context) (*WifiStatus, error) {
	v := &WifiStatus{}
	return v, c.get(ctx, pathWifiStatus, v)
}

// Version reads the part number and the firmware version.
func (c *Client) Version(ctx context.Context) (*Version, error) {
	v := &Version{}
	return v, c.get(ctx, pathVersion, v)
}

// Close closes the connection to the charger, if one is open.
func (c *Client) Close() {
	c.http.CloseIdleConnections()
}

// get sends one GET request and decodes the JSON object of the response
// into v.
func (c *Client) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoResponse, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return fmt.Errorf("GET %s: status %s", path, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: GET %s: %w", ErrNoResponse, path, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("GET %s: the response is more than %d bytes", path, maxResponseBytes)
	}
	if err := decode(data, v); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}

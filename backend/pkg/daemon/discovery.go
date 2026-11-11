// Package daemon owns local service discovery and process-lifetime coordination.
package daemon

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

const (
	// discoverySchemaVersion identifies the ephemeral service-record format.
	discoverySchemaVersion = 1
	// discoveryFileName is the service record clients read to locate the daemon.
	discoveryFileName = "service.json"
	// lockFileName identifies the OS-backed single-daemon ownership lock.
	lockFileName = "service.lock"
)

// Discovery identifies the daemon currently serving one Alfred data directory.
type Discovery struct {
	SchemaVersion uint32    `json:"schema_version"`
	Endpoint      string    `json:"endpoint"`
	InstanceID    string    `json:"instance_id"`
	Version       string    `json:"version"`
	PID           int       `json:"pid"`
	StartedAt     time.Time `json:"started_at"`
}

// ReadDiscovery reads and validates the currently published service record.
func ReadDiscovery(config Config) (Discovery, error) {
	const op = "ReadDiscovery"

	data, err := os.ReadFile(filepath.Join(config.DataDir, discoveryFileName))
	if err != nil {
		builder := errors.B.Op(op).Text("read daemon discovery record").Err(err)
		if errors.Is(err, os.ErrNotExist) {
			builder = builder.KindNotFound()
		}
		return Discovery{}, builder.Build()
	}
	var discovery Discovery
	if err := json.Unmarshal(data, &discovery); err != nil {
		return Discovery{}, errors.B.
			Op(op).
			KindInvalidInput().
			Text("decode daemon discovery record").
			Err(err).
			Build()
	}
	if err := validateDiscovery(discovery); err != nil {
		return Discovery{}, errors.B.Op(op).Err(err).Build()
	}
	return discovery, nil
}

func validateDiscovery(discovery Discovery) error {
	const op = "validateDiscovery"

	if discovery.SchemaVersion != discoverySchemaVersion {
		return errors.B.
			Op(op).
			KindInvalidInput().
			Textf("unsupported daemon discovery schema %d", discovery.SchemaVersion).
			Build()
	}
	if discovery.InstanceID == "" || discovery.Version == "" || discovery.PID <= 0 || discovery.StartedAt.IsZero() {
		return errors.B.Op(op).KindInvalidInput().Text("incomplete daemon discovery record").Build()
	}
	endpoint, err := url.Parse(discovery.Endpoint)
	if err != nil {
		return errors.B.
			Op(op).
			KindInvalidInput().
			Textf("parse daemon endpoint %q", discovery.Endpoint).
			Err(err).
			Build()
	}
	host := endpoint.Hostname()
	address := net.ParseIP(host)
	if endpoint.Scheme != "http" || endpoint.Port() == "" ||
		(host != "localhost" && (address == nil || !address.IsLoopback())) {
		return errors.B.
			Op(op).
			KindInvalidInput().
			Textf("daemon endpoint %q is not local HTTP", discovery.Endpoint).
			Build()
	}
	return nil
}

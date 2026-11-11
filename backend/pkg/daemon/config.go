package daemon

import (
	"net"
	"time"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Config contains runtime configuration for local daemon ownership.
type Config struct {
	DataDir           string        `koanf:"-"`
	ListenAddress     string        `koanf:"listen_address" default:"127.0.0.1:0"`
	ReadHeaderTimeout time.Duration `koanf:"-" default:"5s"`
	ShutdownTimeout   time.Duration `koanf:"-" default:"10s"`
	StartupTimeout    time.Duration `koanf:"-" default:"10s"`
}

// Valid checks that the listener uses a loopback or wildcard host.
func (config Config) Valid() error {
	const op = "Valid"
	host, _, err := net.SplitHostPort(config.ListenAddress)
	ip := net.ParseIP(host)
	if err != nil || (host != "localhost" && (ip == nil || (!ip.IsLoopback() && !ip.IsUnspecified()))) {
		return errors.B.Op(op).KindInvalidArgument().UserMsg("listen_address must use a loopback or wildcard host and a port").Build()
	}
	return nil
}

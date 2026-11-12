package daemon

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"github.com/gofrs/flock"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Stop verifies the local daemon, requests graceful shutdown, and waits until
// shared storage and ownership have been released. An idle directory succeeds.
func Stop(ctx context.Context, config Config) (err error) {
	const op = "Stop"
	ctx, cancel := context.WithTimeout(ctx, config.ShutdownTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	lock := flock.New(filepath.Join(config.DataDir, lockFileName))
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			err = errors.B.Op(op).Text("close daemon ownership probe").Err(err).Err(closeErr).Build()
		}
	}()
	locked, err := lock.TryLock()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.B.Op(op).Text("probe daemon ownership").Err(err).Build()
	}
	if locked {
		return nil
	}
	discovery, err := ReadDiscovery(config)
	if err != nil {
		return errors.B.Op(op).Text("locate daemon to stop").Err(err).Build()
	}
	process, err := os.FindProcess(discovery.PID)
	if err != nil {
		return errors.B.Op(op).Text("find daemon process").Err(err).Build()
	}
	defer func() { _ = process.Release() }()
	// Keep the process handle across identity verification and signaling.
	verified, err := Discover(ctx, config)
	if err != nil {
		return errors.B.Op(op).Text("verify daemon before signaling").Err(err).Build()
	}
	if verified.InstanceID != discovery.InstanceID {
		return errors.B.Op(op).KindConflict().UserMsg("The Alfred instance changed. Try again.").Build()
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.B.Op(op).Text("signal daemon shutdown").Err(err).Build()
	}
	if _, err := lock.TryLockContext(ctx, startupPollInterval); err != nil {
		return errors.B.Op(op).Text("wait for daemon ownership release").UserMsg("Alfred has not finished stopping. No force-kill was sent.").Err(err).Build()
	}
	return nil
}

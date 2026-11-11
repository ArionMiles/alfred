package daemon_test

import (
	"testing"

	"github.com/ArionMiles/alfred/pkg/daemon"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestConfigValidListener(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1:0", "localhost:8080", "[::1]:8080", "0.0.0.0:8080", "[::]:8080"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()
			if err := (daemon.Config{ListenAddress: address}).Valid(); err != nil {
				t.Fatalf("valid listener rejected: %v", err)
			}
		})
	}
	for _, address := range []string{"", "not-an-address", ":8080", "192.168.1.2:8080", "example.com:8080"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()
			err := (daemon.Config{ListenAddress: address}).Valid()
			if errors.WhatKind(err) != errors.InvalidArgument {
				t.Fatalf("invalid listener %q: error = %v", address, err)
			}
		})
	}
}

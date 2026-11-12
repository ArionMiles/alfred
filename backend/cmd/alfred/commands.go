package main

import (
	"github.com/spf13/cobra"

	"github.com/ArionMiles/alfred/internal/buildinfo"
	"github.com/ArionMiles/alfred/internal/config"
	"github.com/ArionMiles/alfred/pkg/daemon"
)

func newRootCommand() *cobra.Command {
	command := &cobra.Command{
		Use:               "alfred",
		Short:             "Alfred personal agent",
		Version:           buildinfo.Version,
		Args:              cobra.NoArgs,
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Usage()
		},
	}
	// Suppress Cobra's standalone help command; bare Alfred already shows usage.
	command.SetHelpCommand(&cobra.Command{Hidden: true})
	command.AddCommand(newTUICommand(), newServeCommand(), newStopCommand(), newRestartCommand())
	return command
}

func newTUICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the terminal client",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, err := config.Load()
			if err != nil {
				return err
			}
			return runClient(command.Context(), settings)
		},
	}
}

func newServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the background service in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, err := config.Load()
			if err != nil {
				return err
			}
			return serve(command.Context(), settings)
		},
	}
}

func newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use: "stop", Short: "Stop the local background service", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, err := config.Load()
			if err != nil {
				return err
			}
			daemonConfig := settings.Daemon
			daemonConfig.DataDir = settings.DataDir
			return daemon.Stop(command.Context(), daemonConfig)
		},
	}
}

func newRestartCommand() *cobra.Command {
	return &cobra.Command{
		Use: "restart", Short: "Restart or start the local background service", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, err := config.Load()
			if err != nil {
				return err
			}
			daemonConfig := settings.Daemon
			daemonConfig.DataDir = settings.DataDir
			if err := daemon.Stop(command.Context(), daemonConfig); err != nil {
				return err
			}
			_, err = daemon.Ensure(command.Context(), daemonConfig, startServiceProcess)
			return err
		},
	}
}

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/ArionMiles/alfred/internal/config"
	"github.com/ArionMiles/alfred/pkg/agent"
	contextbuilder "github.com/ArionMiles/alfred/pkg/context"
	"github.com/ArionMiles/alfred/pkg/daemon"
	"github.com/ArionMiles/alfred/pkg/events"
	"github.com/ArionMiles/alfred/pkg/executor"
	"github.com/ArionMiles/alfred/pkg/invocation"
	"github.com/ArionMiles/alfred/pkg/project"
	"github.com/ArionMiles/alfred/pkg/providers"
	openaiAuthorization "github.com/ArionMiles/alfred/pkg/providers/openai"
	"github.com/ArionMiles/alfred/pkg/skills"
	"github.com/ArionMiles/alfred/pkg/storage/jsonl"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/telemetry"
	"github.com/ArionMiles/alfred/pkg/tools"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/tui"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type application struct {
	server                *daemon.Server
	store                 *sqlite.Store
	providerAuthorization *openaiAuthorization.AuthorizationService
}

func main() {
	os.Exit(exitCode())
}

func exitCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func run(ctx context.Context, arguments []string) error {
	const op = "run"

	if len(arguments) > 1 || (len(arguments) == 1 && arguments[0] != "serve") {
		return errors.B.Op(op).KindInvalidArgument().UserMsg("usage: alfred [serve]").Build()
	}
	settings, err := config.Load()
	if err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	if len(arguments) == 1 {
		if err := runService(ctx, settings); err != nil {
			return errors.B.Op(op).Text("run Alfred service").Err(err).Build()
		}
		return nil
	}
	if err := runClient(ctx, settings); err != nil {
		return errors.B.Op(op).Text("run Alfred client").Err(err).Build()
	}
	return nil
}

func runService(ctx context.Context, settings config.C) error {
	const op = "runService"

	app, err := compose(ctx, settings)
	if err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	if err := app.Serve(ctx); err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	return nil
}

func runClient(ctx context.Context, settings config.C) error {
	const op = "runClient"

	daemonConfig := settings.Daemon
	daemonConfig.DataDir = settings.DataDir
	discovery, err := daemon.Ensure(ctx, daemonConfig, startServiceProcess)
	if err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	root, err := os.Getwd()
	if err != nil {
		return errors.B.Op(op).Text("get current working directory").Err(err).Build()
	}
	if err := tui.Run(ctx, settings.TUI, rpcClients(discovery.Endpoint), root); err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	return nil
}

func compose(ctx context.Context, settings config.C) (app *application, err error) {
	const op = "compose"

	daemonConfig := settings.Daemon
	daemonConfig.DataDir = settings.DataDir
	owner, err := daemon.Acquire(daemonConfig)
	if err != nil {
		return nil, errors.B.Op(op).Text("acquire daemon ownership").Err(err).Build()
	}
	defer func() {
		if err == nil {
			return // Successful composition transfers ownership to the application.
		}
		if closeErr := owner.Close(); closeErr != nil {
			err = errors.B.Op(op).Text("release daemon ownership after failed composition").Err(err).Err(closeErr).Build()
		}
	}()
	store, err := sqlite.Open(ctx, sqlite.Config{
		Path:        filepath.Join(settings.DataDir, "alfred.db"),
		BusyTimeout: settings.SQLite.BusyTimeout,
	})
	if err != nil {
		return nil, errors.B.Op(op).Text("open SQLite store").Err(err).Build()
	}

	defer func() {
		if err == nil {
			return
		}
		if closeErr := store.Close(); closeErr != nil {
			err = errors.B.Op(op).Text("close SQLite store after failed composition").Err(err).Err(closeErr).Build()
		}
	}()

	broker := events.NewBroker()
	eventStore := jsonl.New(
		jsonl.Config{DataDir: settings.DataDir, LockShardCount: settings.History.LockShardCount},
		store,
		store,
	)
	eventSink := events.NewNotifyingEventSink(eventStore, broker)
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.B.Op(op).Text("resolve user home").Err(err).Build()
	}
	searchTool, err := tools.NewWebSearch(settings.Tools.WebSearch, tools.NewExa(settings.Tools.WebSearch.Exa, store, store))
	if err != nil {
		return nil, errors.B.Op(op).Text("create web search").Err(err).Build()
	}
	toolRegistry, err := tools.NewRegistry(tools.NewRead(), tools.NewGrep(), tools.NewGlob(), tools.NewEdit(),
		tools.NewShell(settings.Tools.Shell, userHome), tools.NewAskUser(), tools.NewWebFetch(settings.Tools.WebFetch), searchTool)
	if err != nil {
		return nil, errors.B.Op(op).Text("create tool registry").Err(err).Build()
	}
	providerRegistry, err := providers.NewRegistry(
		nil,
		providers.WithConfiguredProviders(openaiAuthorization.NewResolver(
			store, store,
			openaiAuthorization.NewSubscriptionCredentials(
				store, settings.OpenAIAuthorization,
			),
			providers.NewCatalogSource(settings.Catalog, settings.DataDir),
		)),
		providers.WithMiddleware(telemetry.ModelCalls(eventSink)),
	)
	if err != nil {
		return nil, errors.B.Op(op).Text("create provider registry").Err(err).Build()
	}
	skillResolver := skills.NewResolver(skills.Config{
		UserHome:   userHome,
		ConfigHome: filepath.Dir(settings.ConfigDir),
		SystemRoot: settings.Skills.SystemRoot,
	}, store)
	runner := agent.NewRunner(agent.Dependencies{
		Runs:        store,
		Sessions:    store,
		Invocations: store,
		Providers:   providerRegistry,
		Skills:      skillResolver,
		Contexts:    contextbuilder.New(contextbuilder.Config{ConfigDir: settings.ConfigDir}, toolRegistry, eventStore, store),
		Executor:    executor.New(toolRegistry, store),
		Events:      eventSink,
		EventReader: eventStore,
		Broadcaster: broker,
	})
	dispatcher := agent.NewDispatcher(ctx, runner)
	invocationService := invocation.NewService(
		invocation.Dependencies{
			ModelSelection: store,
			Projects:       store,
			Sessions:       store,
			Invocations:    store,
			Runs:           store,
			Dispatcher:     dispatcher,
			Events:         eventSink,
		},
	)
	projectService := project.NewService(store)
	providerSetup, err := providers.NewSetupService(providers.SetupDefinitions())
	if err != nil {
		return nil, errors.B.Op(op).Text("create provider setup service").Err(err).Build()
	}
	providerSubmitter := providers.NewSubmitter(providerSetup, store)
	providerAuthorization := openaiAuthorization.NewAuthorizationService(
		ctx,
		settings.OpenAIAuthorization,
		openaiAuthorization.AuthorizationDependencies{
			Store: store, Credentials: store,
		},
	)
	defer func() {
		if err != nil {
			providerAuthorization.Close()
		}
	}()
	modelService := providers.NewModelService(store, providerRegistry, store)
	status := rpc.NewDaemonHandler(owner.InstanceID(), owner.StartedAt())
	handler := rpc.NewServer(rpc.Handlers{
		SearchSetup:   rpc.NewSearchSetupHandler(tools.NewSearchSetup(tools.SearchSetupDefinitions(), store, store)),
		Daemon:        status,
		Project:       rpc.NewProjectHandler(projectService, skills.NewInspector(store), store),
		Invocation:    rpc.NewInvocationHandler(invocationService),
		Session:       rpc.NewSessionHandler(store, eventStore, broker),
		Run:           rpc.NewRunHandler(store, dispatcher),
		ProviderSetup: rpc.NewProviderSetupHandler(providerSetup, providerSubmitter, store, providerAuthorization),
		Models:        rpc.NewModelHandler(modelService),
	})
	server, err := daemon.NewServer(ctx, daemonConfig, owner, handler, status)
	if err != nil {
		return nil, errors.B.Op(op).Text("create daemon server").Err(err).Build()
	}
	return &application{server: server, store: store, providerAuthorization: providerAuthorization}, nil
}

func (app *application) Serve(ctx context.Context) error {
	const op = "Serve"

	serveErr := app.server.Serve(ctx)
	app.providerAuthorization.Close()
	closeErr := app.store.Close()
	if serveErr != nil || closeErr != nil {
		return errors.B.Op(op).Err(serveErr).Err(closeErr).Build()
	}
	return nil
}

func startServiceProcess() error {
	const op = "startServiceProcess"

	executable, err := os.Executable()
	if err != nil {
		return errors.B.Op(op).Text("locate Alfred executable").Err(err).Build()
	}
	// #nosec G204 -- executable is the path of the currently running Alfred binary.
	command := exec.CommandContext(context.Background(), executable, "serve")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return errors.B.Op(op).Text("start Alfred service process").Err(err).Build()
	}
	if err := command.Process.Release(); err != nil {
		return errors.B.Op(op).Text("release Alfred service process").Err(err).Build()
	}
	return nil
}

func rpcClients(endpoint string) rpc.Clients {
	httpClient := &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return rpc.NewClients(connect.NewClient(connecthttp.NewTransport(httpClient, endpoint)))
}

// Command minimal is the smallest runnable Chame application (spec §19
// Phase 1): a runtime, one custom module with a capability and a health
// check, running until interrupted. It needs no chain access.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/health"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/core/lifecycle"
	"github.com/cuihairu/chame/core/logging"
	"github.com/cuihairu/chame/core/module"
	"github.com/cuihairu/chame/core/runtime"
)

// greeter is an application module: it declares a capability, serves a
// health check, and logs on start and stop. Business modules look like
// this; the framework never defines the business itself.
type greeter struct{}

func (g *greeter) Name() string { return "greeter" }

func (g *greeter) Register(reg module.Registry) error {
	if err := reg.PutCapability(capability.Network, fakeNetwork{}); err != nil {
		return err
	}
	return reg.AddHealthCheck("greeter", health.CheckFunc(func(ctx context.Context) (health.Status, error) {
		return health.Healthy, nil
	}))
}

func (g *greeter) Start(ctx context.Context) error {
	slog.Info("greeter is up")
	return nil
}

func (g *greeter) Stop(ctx context.Context) error {
	slog.Info("greeter is down")
	return nil
}

type fakeNetwork struct{}

func (fakeNetwork) Network() identifiers.NetworkID {
	return identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "example", ChainID: "1"}
}

func (fakeNetwork) Healthy(ctx context.Context) error { return nil }

func main() {
	// Configuration is constructed explicitly; file and env layers are
	// optional extras (spec §5.1).
	res, err := config.NewLoader().
		Defaults(config.Default()).
		Env().
		Override(func(c *config.Config) { c.App.Name = "minimal-example" }).
		Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	logger, err := logging.New(logging.Options{Level: res.Config.Logging.Level, Format: res.Config.Logging.Format})
	if err != nil {
		fmt.Fprintln(os.Stderr, "logging:", err)
		os.Exit(1)
	}

	rt := runtime.New(runtime.WithLogger(logger), runtime.WithShutdownTimeout(res.Config.App.ShutdownTimeout))
	if err := rt.Register(&greeter{}, module.Info{Provides: []capability.Name{capability.Network}}); err != nil {
		fmt.Fprintln(os.Stderr, "register:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("running; press Ctrl-C to stop", slog.String("state", lifecycle.StateNew.String()))
	if err := rt.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "runtime:", err)
		os.Exit(1)
	}
	slog.Info("clean shutdown")
}

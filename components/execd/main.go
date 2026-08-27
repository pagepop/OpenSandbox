// Copyright 2025 Alibaba Group Holding Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alibaba/opensandbox/internal/version"

	_ "github.com/alibaba/opensandbox/internal/safego"
	_ "go.uber.org/automaxprocs/maxprocs"

	"github.com/alibaba/opensandbox/execd/pkg/clone3compat"
	"github.com/alibaba/opensandbox/execd/pkg/flag"
	"github.com/alibaba/opensandbox/execd/pkg/isolation"
	"github.com/alibaba/opensandbox/execd/pkg/log"
	"github.com/alibaba/opensandbox/execd/pkg/runtime"
	"github.com/alibaba/opensandbox/execd/pkg/telemetry"
	"github.com/alibaba/opensandbox/execd/pkg/web"
	"github.com/alibaba/opensandbox/execd/pkg/web/controller"
)

func main() {
	if err := run(); err != nil {
		log.Error("execd failed: %v", err)
		os.Exit(1)
	}
}

func run() error {
	clone3Compat := clone3compat.MaybeApply()

	version.EchoVersion("OpenSandbox Execd")

	flag.InitFlags()

	// Load isolation config.
	isoCfg, err := isolation.LoadConfig(flag.IsolationConfigPath)
	if err != nil {
		return fmt.Errorf("isolation config: %w", err)
	}

	// Probe isolation runtime capabilities.
	isolationProbe := isolation.Probe(isolation.ProbeConfig{
		UpperRoot:     isoCfg.UpperRoot,
		UpperMaxBytes: isoCfg.UpperMaxBytes,
	})
	log.Info("isolation: available=%v isolator=%s version=%s",
		isolationProbe.Available, isolationProbe.Isolator, isolationProbe.Version)

	log.Init(flag.ServerLogLevel)

	ctrl := controller.InitCodeRunner()

	// Always store probe result for capabilities endpoint.
	controller.InitIsolatedProbe(&isolationProbe)

	// Init isolation runner if probe succeeded.
	if isolationProbe.Available {
		iso := isolation.NewBwrap(isoCfg)
		runner, err := runtime.NewIsolatedRunner(ctrl, iso, isoCfg)
		if err != nil {
			log.Error("isolation: runner init failed (continuing without isolation): %v", err)
		} else {
			controller.InitIsolatedRunner(runner)
			log.Info("isolation: runner ready, upper_root=%s", isoCfg.UpperRoot)
		}
	}
	if clone3Compat {
		log.Warn("execd running with clone3 compatibility (seccomp returns ENOSYS for clone3)")
	}
	otelShutdown, err := telemetry.Init(context.Background())
	if err != nil {
		log.Warn("OpenTelemetry metrics disabled (continuing without OTLP): %v", err)
		otelShutdown = nil
	}
	if otelShutdown != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = otelShutdown(shutdownCtx)
		}()
	}

	processManager := runtime.NewManagedProcessManager()
	terminalManager := runtime.NewManagedTerminalManager()
	engine := web.NewRouter(flag.ServerAccessToken, processManager, terminalManager)
	addr := fmt.Sprintf(":%d", flag.ServerPort)
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	log.Info("execd listening on %s (IPv4)", addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: engine}
	return serveExecd(ctx, server, listener, processManager, terminalManager, flag.ApiGracefulShutdownTimeout)
}

func serveExecd(
	ctx context.Context,
	server *http.Server,
	listener net.Listener,
	processManager *runtime.ManagedProcessManager,
	terminalManager *runtime.ManagedTerminalManager,
	shutdownTimeout time.Duration,
) error {
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		log.Info("received shutdown signal; stopping managed runtimes")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := shutdownExecd(shutdownCtx, server, processManager, terminalManager)
		if deadlineErr := shutdownCtx.Err(); deadlineErr != nil {
			closeErr := server.Close()
			return errors.Join(shutdownErr, closeErr, fmt.Errorf("execd shutdown deadline: %w", deadlineErr))
		}
		select {
		case serveErr := <-serveDone:
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				return errors.Join(shutdownErr, fmt.Errorf("execd server shutdown: %w", serveErr))
			}
			return shutdownErr
		case <-shutdownCtx.Done():
			closeErr := server.Close()
			return errors.Join(shutdownErr, closeErr, fmt.Errorf("execd shutdown deadline: %w", shutdownCtx.Err()))
		}
	case serveErr := <-serveDone:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := shutdownExecd(shutdownCtx, server, processManager, terminalManager)
		if deadlineErr := shutdownCtx.Err(); deadlineErr != nil {
			shutdownErr = errors.Join(
				shutdownErr,
				server.Close(),
				fmt.Errorf("execd shutdown deadline: %w", deadlineErr),
			)
		}
		return errors.Join(fmt.Errorf("execd server stopped unexpectedly: %w", serveErr), shutdownErr)
	}
}

func shutdownExecd(
	ctx context.Context,
	server *http.Server,
	processManager *runtime.ManagedProcessManager,
	terminalManager *runtime.ManagedTerminalManager,
) error {
	processDone := make(chan error, 1)
	terminalDone := make(chan error, 1)
	go func() { processDone <- processManager.Shutdown(ctx) }()
	go func() { terminalDone <- terminalManager.Shutdown(ctx) }()
	processErr := <-processDone
	terminalErr := <-terminalDone
	httpErr := server.Shutdown(ctx)

	var shutdownErrs []error
	if processErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("managed process shutdown: %w", processErr))
	}
	if terminalErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("managed terminal shutdown: %w", terminalErr))
	}
	if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("HTTP server shutdown: %w", httpErr))
	}
	return errors.Join(shutdownErrs...)
}

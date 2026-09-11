// Command autoroute is the OpenAI-compatible routing proxy.
//
// M1: it forwards /v1/chat/completions to the provider named by the model
// catalogue and relays the response (streaming or unary), with /healthz,
// /readyz and Prometheus /metrics. Routing arrives in M2.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/provider"
	"github.com/ngadakh/autoroute/internal/proxy"
)

// version is overridable at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	addr := flag.String("addr", envOr("AUTOROUTE_ADDR", ":8080"), "listen address")
	cataloguePath := flag.String("catalogue", envOr("AUTOROUTE_CATALOGUE", "configs/catalogue.yaml"), "path to the model catalogue")
	logJSON := flag.Bool("log-json", false, "emit structured JSON logs")
	shutdownGrace := flag.Duration("shutdown-grace", 20*time.Second, "max time to drain on SIGTERM")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		os.Stdout.WriteString("autoroute " + version + "\n")
		return
	}

	logger := newLogger(*logJSON)
	slog.SetDefault(logger)

	if err := run(*addr, *cataloguePath, *shutdownGrace, logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(addr, cataloguePath string, grace time.Duration, logger *slog.Logger) error {
	cat, err := config.Load(cataloguePath)
	if err != nil {
		return err
	}

	httpClient := &http.Client{
		Timeout: 0, // streaming; per-request deadlines come from the client context
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ExpectContinueTimeout: time.Second,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		},
	}
	providers, err := provider.Build(cat, httpClient)
	if err != nil {
		return err
	}
	metrics := observability.New(version)

	srv := proxy.New(cat, providers, metrics, logger, version)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening",
			"addr", addr, "version", version,
			"models", cat.ModelNames(), "catalogue", cataloguePath)
		srv.SetReady(true)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining", "grace", grace)
		srv.SetReady(false) // fail /readyz so LBs stop sending new traffic

		shCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := httpSrv.Shutdown(shCtx); err != nil {
			return err
		}
		logger.Info("stopped cleanly")
		return nil
	}
}

func newLogger(jsonOut bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if jsonOut {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

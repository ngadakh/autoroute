// Command autoroute is the OpenAI-compatible routing proxy.
//
// It forwards /v1/chat/completions to the provider named by the model
// catalogue and relays the response (streaming or unary), with /healthz,
// /readyz and Prometheus /metrics. Since M2, a catalogue with a `router:`
// block also routes requests naming router.trigger_model (e.g. "auto") to a
// tier via L1 heuristics + an optional L2 embedding classifier — see
// internal/router. The default build (CGO_ENABLED=0) runs L1-only; `make
// build-router` (CGO_ENABLED=1, after `make setup`) adds L2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/provider"
	"github.com/ngadakh/autoroute/internal/proxy"
	"github.com/ngadakh/autoroute/internal/router"
)

// version is overridable at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	addr := flag.String("addr", envOr("AUTOROUTE_ADDR", ":8080"), "listen address")
	cataloguePath := flag.String("catalogue", envOr("AUTOROUTE_CATALOGUE", "configs/catalogue.yaml"), "path to the model catalogue")
	decisionLogPath := flag.String("decision-log", envOr("AUTOROUTE_DECISION_LOG", "data/decisions.jsonl"), "append-only router decision log (JSON lines); empty disables it")
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

	if err := run(*addr, *cataloguePath, *decisionLogPath, *shutdownGrace, logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(addr, cataloguePath, decisionLogPath string, grace time.Duration, logger *slog.Logger) error {
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

	if cat.Router != nil && cat.Router.Enabled {
		pipeline, tierModels := buildRouter(cat.Router, logger)
		srv.Router = pipeline
		srv.TierModels = tierModels
		srv.TriggerModel = cat.Router.TriggerModel

		if decisionLogPath != "" {
			f, err := openDecisionLog(decisionLogPath)
			if err != nil {
				return err
			}
			defer f.Close()
			srv.DecisionLog = observability.NewDecisionLog(f)
		}
	}

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

// buildRouter constructs the layered-router Pipeline from catalogue config.
// The L2 embedding classifier may be unavailable (CGO_ENABLED=0 build, or
// embedding.model_path/vocab_path unset) — that's logged, not fatal; the
// pipeline still runs L1-only and degrades every L1 miss to DefaultTier.
func buildRouter(rc *config.RouterConfig, logger *slog.Logger) (*router.Pipeline, map[router.Tier]string) {
	tierModels := make(map[router.Tier]string, len(rc.Tiers))
	for tier, model := range rc.Tiers {
		tierModels[router.Tier(tier)] = model
	}

	clf, embedder, err := router.BuildClassifier(rc.Embedding)
	if err != nil {
		logger.Warn("L2 embedding classifier unavailable, router running L1-only", "err", err)
	}

	pipeline := router.NewPipeline(clf, embedder, rc.ThetaLow, rc.ThetaHigh, router.Tier(rc.DefaultTier))
	return pipeline, tierModels
}

// openDecisionLog opens the router decision log for append, creating its
// parent directory if needed.
func openDecisionLog(path string) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create decision log directory: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open decision log: %w", err)
	}
	return f, nil
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

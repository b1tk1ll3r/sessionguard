package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/sessionguard/internal/edgeguard"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "/etc/sessionguard-edgeguard/edgeguard.json", "path to EdgeGuard JSON configuration")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sessionguard-edgeguard %s\n", version)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := edgeguard.LoadRuntimeConfig(*configPath)
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(2)
	}
	if *checkConfig {
		fmt.Printf("configuration OK (%s)\n", cfg.Fingerprint()[:12])
		return
	}

	guard := edgeguard.NewGuard(cfg, logger)
	handler := edgeguard.NewHTTPServer(guard, logger).Handler()
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go reloadLoop(ctx, *configPath, guard, logger)
	go cleanupLoop(ctx, guard)
	go persistenceLoop(ctx, guard)

	go func() {
		<-ctx.Done()
		guard.FlushState()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("SessionGuard EdgeGuard started", "version", version, "listen", cfg.Listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("edgeguard server stopped", "error", err)
		os.Exit(1)
	}
}

func reloadLoop(ctx context.Context, path string, guard *edgeguard.Guard, logger *slog.Logger) {
	interval := guard.Config().ReloadInterval()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cfg, err := edgeguard.LoadRuntimeConfig(path)
			if err != nil {
				logger.Error("reload edgeguard configuration", "error", err)
				continue
			}
			guard.ReplaceConfig(cfg)
			newInterval := cfg.ReloadInterval()
			if newInterval != interval {
				interval = newInterval
				t.Reset(interval)
			}
		}
	}
}

func cleanupLoop(ctx context.Context, guard *edgeguard.Guard) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			guard.Cleanup(now)
		}
	}
}

func persistenceLoop(ctx context.Context, guard *edgeguard.Guard) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			guard.FlushState()
		}
	}
}

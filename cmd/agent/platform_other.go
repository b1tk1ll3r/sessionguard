//go:build !windows

package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/example/sessionguard/internal/agent"
	"github.com/example/sessionguard/internal/config"
)

func defaultConfigPath() string { return "./agent.json" }
func platformMain(action, path string) error {
	if action != "" && action != "run" {
		return fmt.Errorf("Windows service actions are only available on Windows")
	}
	cfg, err := config.LoadAgent(path)
	if err != nil {
		return err
	}
	app, err := agent.New(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx)
}

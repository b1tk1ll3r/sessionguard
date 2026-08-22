package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/master"
)

func main() {
	cfgPath := flag.String("config", "/etc/sessionguard/master.json", "master config path")
	flag.Parse()
	cfg, err := config.LoadMaster(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	app, err := master.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	cfg := flag.String("config", defaultConfigPath(), "agent config path")
	action := flag.String("service", "", "service action: install|uninstall|start|stop|run")
	flag.Parse()
	if err := platformMain(*action, *cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

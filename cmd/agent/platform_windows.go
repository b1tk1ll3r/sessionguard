//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/example/sessionguard/internal/agent"
	"github.com/example/sessionguard/internal/config"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "SessionGuardAgent"

func defaultConfigPath() string { return `C:\ProgramData\SessionGuard\agent.json` }

func platformMain(action, path string) error {
	switch action {
	case "install":
		return installService(path)
	case "uninstall":
		return uninstallService()
	case "start":
		return startService()
	case "stop":
		return stopService()
	case "run":
		return runAsService(path)
	case "":
		isSvc, err := svc.IsWindowsService()
		if err == nil && isSvc {
			return runAsService(path)
		}
		return runConsole(path)
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
}

func setupLog(dataDir string) {
	_ = os.MkdirAll(dataDir, 0o700)
	f, err := os.OpenFile(filepath.Join(dataDir, "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
}
func loadApp(path string) (*agent.App, error) {
	cfg, err := config.LoadAgent(path)
	if err != nil {
		return nil, err
	}
	setupLog(cfg.DataDir)
	return agent.New(cfg)
}
func runConsole(path string) error {
	app, err := loadApp(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx)
}

type serviceHandler struct{ path string }

func (h *serviceHandler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}
	app, err := loadApp(h.path)
	if err != nil {
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: accepts}
	for {
		select {
		case c := <-requests:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			}
		case err := <-done:
			if err != nil {
				log.Printf("service stopped: %v", err)
				return false, 1
			}
			return false, 0
		}
	}
}
func runAsService(path string) error { return svc.Run(serviceName, &serviceHandler{path: path}) }
func installService(path string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("service already exists")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	s, err := m.CreateService(serviceName, exe, mgr.Config{DisplayName: "SessionGuard Agent", Description: "SessionGuard Terminal Server Agent", StartType: mgr.StartAutomatic}, "-config", path, "-service", "run")
	if err != nil {
		return err
	}
	defer s.Close()
	return nil
}
func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Delete()
}
func startService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Start()
}
func stopService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer s.Close()
	_, err = s.Control(svc.Stop)
	return err
}

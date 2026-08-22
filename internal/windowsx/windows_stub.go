//go:build !windows

package windowsx

import (
	"errors"
	"github.com/example/sessionguard/internal/model"
)

var ErrUnsupported = errors.New("Windows functionality is only available on Windows")

func Sessions() ([]model.Session, error)       { return nil, ErrUnsupported }
func Processes() ([]model.ProcessInfo, error)  { return nil, ErrUnsupported }
func Server() (model.ServerInfo, error)        { return model.ServerInfo{}, ErrUnsupported }
func ProfilePath(string) (string, error)       { return "", ErrUnsupported }
func DeleteProfile(string) error               { return ErrUnsupported }
func LogoffSession(uint32) error               { return ErrUnsupported }
func DisconnectSession(uint32) error           { return ErrUnsupported }
func SendMessage(uint32, string, string) error { return ErrUnsupported }
func TerminateProcess(uint32) error            { return ErrUnsupported }
func RestartServer(string) error               { return ErrUnsupported }
func MachineID() (string, error)               { return "nonwindows", nil }

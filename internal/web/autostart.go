package web

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// The registry key lives under HKCU so the toggle needs no administrator
// rights, and it only affects the current user's login session.
const autoStartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autoStartValueName = "M365-Copilot2API"

func autoStartSupported() bool { return runtime.GOOS == "windows" }

func autoStartExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// setAutoStart registers or removes a current-user Run entry pointing at the
// running executable. The workdir is inherited from the executable path; a
// wrapper script is not involved, so no console window logic is needed here.
func setAutoStart(enabled bool) error {
	if !autoStartSupported() {
		return fmt.Errorf("auto start is only supported on windows")
	}
	exe, err := autoStartExecutable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	return registrySetStringValue(autoStartRunKey, autoStartValueName, enabled, exe)
}

func autoStartEnabled() bool {
	if !autoStartSupported() {
		return false
	}
	v, err := registryGetStringValue(autoStartRunKey, autoStartValueName)
	if err != nil {
		return false
	}
	exe, err := autoStartExecutable()
	if err != nil {
		return true
	}
	return v == exe
}

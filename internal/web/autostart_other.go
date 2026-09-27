//go:build !windows

package web

import "fmt"

// Non-Windows builds never reach the registry paths because
// autoStartSupported() returns false, but the package must still compile on
// linux/darwin/freebsd and the other release targets.

func registrySetStringValue(keyPath, valueName string, enabled bool, data string) error {
	return fmt.Errorf("auto start is only supported on windows")
}

func registryGetStringValue(keyPath, valueName string) (string, error) {
	return "", fmt.Errorf("auto start is only supported on windows")
}

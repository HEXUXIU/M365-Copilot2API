package web

import (
	"fmt"
	"os/exec"
	"strings"
)

// registrySetStringValue writes/deletes an HKCU Run value via reg.exe.
// The registry API from golang.org/x/sys returned "Access is denied" on some
// Windows 10 setups even for HKCU, while reg.exe (the same elevation) works;
// we use the proven path and keep the API surface identical.
func registrySetStringValue(keyPath, valueName string, enabled bool, data string) error {
	full := `HKCU\` + keyPath
	if !enabled {
		cmd := exec.Command("reg", "delete", full, "/v", valueName, "/f")
		if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(strings.ToLower(string(out)), "unable to find") {
			// Missing value is success (idempotent delete); anything else fails.
			if err != nil && strings.Contains(string(out), "操作成功完成") {
				return nil
			}
			// reg delete returns success text in Chinese/English; treat
			// "unable to find" variants as success, real errors as failure.
			if err != nil && !isRegistryNotFound(out) {
				return fmt.Errorf("reg delete: %v: %s", err, strings.TrimSpace(string(out)))
			}
		}
		return nil
	}
	cmd := exec.Command("reg", "add", full, "/v", valueName, "/t", "REG_SZ", "/d", data, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func registryGetStringValue(keyPath, valueName string) (string, error) {
	full := `HKCU\` + keyPath
	out, err := exec.Command("reg", "query", full, "/v", valueName).CombinedOutput()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, valueName) {
			parts := strings.SplitN(strings.TrimSpace(line), "REG_SZ", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}
	return "", fmt.Errorf("value %s not found", valueName)
}

func isRegistryNotFound(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "unable to find") ||
		strings.Contains(s, "unable to locate") ||
		strings.Contains(s, "找不到") ||
		strings.Contains(s, "not found")
}

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Account struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName,omitempty"`
	Status      string `json:"status"`
}

type Store struct {
	Accounts []Account `json:"accounts"`
}

func Path() string {
	if p := os.Getenv("M365_CONFIG"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "m365-native", "accounts.json")
}

func Load() (Store, error) {
	b, e := os.ReadFile(Path())
	if os.IsNotExist(e) {
		return Store{Accounts: []Account{}}, nil
	}
	if e != nil {
		return Store{}, e
	}
	var s Store
	e = json.Unmarshal(b, &s)
	return s, e
}

func Save(s Store) error {
	p := Path()
	if e := os.MkdirAll(filepath.Dir(p), 0o700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(p, b, 0o600)
}

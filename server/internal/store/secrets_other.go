//go:build !windows

package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Outside Windows the secret store is a file only the current user can read.
const secretsFile = "secrets.json"

func loadSecrets(dir string) (map[string]string, error) {
	out := map[string]string{}
	path := filepath.Join(dir, secretsFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		os.Chmod(path, 0o600)
	}
	return out, json.Unmarshal(raw, &out)
}

func saveSecrets(dir string, m map[string]string) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, secretsFile), raw, 0o600)
}

func SecretBackend() string { return "file readable only by the current user (0600)" }

func SystemLang() string { return langFromEnv() }

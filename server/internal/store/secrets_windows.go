//go:build windows

package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the secret store is a DPAPI blob bound to the current user account.
const secretsFile = "secrets.dpapi"

func loadSecrets(dir string) (map[string]string, error) {
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(dir, secretsFile))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	plain, err := dpapi(raw, false)
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(plain, &out)
}

func saveSecrets(dir string, m map[string]string) error {
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	sealed, err := dpapi(plain, true)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, secretsFile), sealed, 0o600)
}

func dpapi(in []byte, protect bool) ([]byte, error) {
	if len(in) == 0 {
		in = []byte{0}
	}
	src := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	var dst windows.DataBlob
	var err error
	if protect {
		err = windows.CryptProtectData(&src, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &dst)
	} else {
		err = windows.CryptUnprotectData(&src, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &dst)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(dst.Data)))
	return append([]byte(nil), unsafe.Slice(dst.Data, dst.Size)...), nil
}

// SecretBackend names the protection used, for "droidline doctor".
func SecretBackend() string { return "Windows DPAPI (current user)" }

func SystemLang() string {
	if v := os.Getenv("DROIDLINE_LANG"); v != "" {
		return langCode(v)
	}
	names, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(names) == 0 {
		return langFromEnv()
	}
	return langCode(names[0])
}

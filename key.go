package main

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kirsle/configdir"
)

var (
	keyPath = path.Join(configdir.LocalConfig("xmit"), "key")
)

func findKey() string {
	if key, found := os.LookupEnv("XMIT_KEY"); found {
		return key
	}
	if b, err := os.ReadFile(keyPath); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func storeKey(key string) error {
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return err
	}
	return os.WriteFile(keyPath, []byte(key), 0600)
}

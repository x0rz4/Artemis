package server

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestJWTKeyPersistence(t *testing.T) {
	keyDir := filepath.Join(t.TempDir(), "auth")
	dataDir := t.TempDir()
	key, err := loadOrCreateJWTKey(keyDir, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signJWT(key)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadOrCreateJWTKey(keyDir, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, reloaded) || !verifyJWT(token, reloaded) {
		t.Fatal("reloading the persisted key invalidated an existing token")
	}
	if _, err := os.Stat(filepath.Join(dataDir, jwtKeyFilename)); !os.IsNotExist(err) {
		t.Fatalf("signing key must not appear in the workspace: %v", err)
	}
	info, err := os.Stat(filepath.Join(keyDir, jwtKeyFilename))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("key permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestJWTKeyMigration(t *testing.T) {
	keyDir := filepath.Join(t.TempDir(), "auth")
	dataDir := t.TempDir()
	legacy := filepath.Join(dataDir, jwtKeyFilename)
	key := []byte(strings.Repeat("a", 32))
	if err := os.WriteFile(legacy, key, 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := signJWT(key)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := loadOrCreateJWTKey(keyDir, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, migrated) || !verifyJWT(token, migrated) {
		t.Fatal("migration invalidated the original token")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy key remains in browsable workspace: %v", err)
	}
	persisted, err := os.ReadFile(filepath.Join(keyDir, jwtKeyFilename))
	if err != nil || !bytes.Equal(key, persisted) {
		t.Fatalf("migrated key was not persisted: %v", err)
	}
}

func TestJWTKeyFailure(t *testing.T) {
	for _, location := range []string{"current", "legacy"} {
		t.Run(location+" corrupt", func(t *testing.T) {
			keyDir, dataDir := t.TempDir(), t.TempDir()
			dir := keyDir
			if location == "legacy" {
				dir = dataDir
			}
			path := filepath.Join(dir, jwtKeyFilename)
			original := []byte("truncated-key")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadOrCreateJWTKey(keyDir, dataDir); err == nil {
				t.Fatal("corrupt key must fail instead of silently rotating")
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("corrupt key was modified: %v", err)
			}
		})
	}
	t.Run("unreadable key", func(t *testing.T) {
		keyDir := t.TempDir()
		// A directory at the key path produces a read failure even when run as root.
		if err := os.Mkdir(filepath.Join(keyDir, jwtKeyFilename), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := loadOrCreateJWTKey(keyDir, t.TempDir()); err == nil {
			t.Fatal("unreadable key must fail")
		}
	})
	t.Run("blocked directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth")
		if err := os.WriteFile(path, []byte("occupied"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadOrCreateJWTKey(path, t.TempDir()); err == nil {
			t.Fatal("invalid key directory must fail")
		}
	})
}

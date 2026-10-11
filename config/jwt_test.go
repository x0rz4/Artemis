package config

import "testing"

func TestJWTKeyDir(t *testing.T) {
	t.Setenv("ARTEX_JWT_KEY_DIR", "")
	if got := JWTKeyDir(); got != BaseDir() {
		t.Fatalf("default key directory = %q, want %q", got, BaseDir())
	}
	dir := t.TempDir()
	t.Setenv("ARTEX_JWT_KEY_DIR", dir)
	if got := JWTKeyDir(); got != dir {
		t.Fatalf("configured key directory = %q, want %q", got, dir)
	}
}

package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("HOKAN_DATA_DIR", "")
	t.Setenv("HOKAN_DB_PATH", "")
	t.Setenv("HOKAN_HTTP_ADDR", "")
	t.Setenv("HOKAN_SSH_ADDR", "")
	t.Setenv("HOKAN_BASE_URL", "")
	t.Setenv("HOKAN_SSH_HOST_KEY", "")
	t.Setenv("HOKAN_ALLOW_SIGNUP", "")

	cfg := Load()
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.SSHAddr != ":2222" {
		t.Fatalf("SSHAddr = %q", cfg.SSHAddr)
	}
	if cfg.DataDir != "./data/repos" {
		t.Fatalf("DataDir = %q", cfg.DataDir)
	}
	if !cfg.AllowSignup {
		t.Fatal("AllowSignup should default true")
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("HOKAN_HTTP_ADDR", ":9090")
	t.Setenv("HOKAN_ALLOW_SIGNUP", "false")
	cfg := Load()
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.AllowSignup {
		t.Fatal("AllowSignup should be false")
	}
}

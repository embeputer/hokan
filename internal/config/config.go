package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DataDir     string
	DBPath      string
	HTTPAddr    string
	SSHAddr     string
	BaseURL     string
	SSHHostKey  string
	AllowSignup bool
}

func Load() Config {
	c := Config{
		DataDir:     env("HOKAN_DATA_DIR", "./data/repos"),
		DBPath:      env("HOKAN_DB_PATH", "./data/hokan.db"),
		HTTPAddr:    env("HOKAN_HTTP_ADDR", ":8080"),
		SSHAddr:     env("HOKAN_SSH_ADDR", ":2222"),
		BaseURL:     env("HOKAN_BASE_URL", "http://localhost:8080"),
		SSHHostKey:  env("HOKAN_SSH_HOST_KEY", "./data/ssh_host_key"),
		AllowSignup: envBool("HOKAN_ALLOW_SIGNUP", true),
	}
	return c
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

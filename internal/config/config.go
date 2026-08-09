// Package config loads app configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// ListenAddr is the address the HTTP server binds to.
	ListenAddr string

	// NVRPath is the root directory containing the UniFi-Protect_YYYY-MM-DD
	// day folders (read-only bind mount).
	NVRPath string
	// DataPath holds the SQLite database, thumbnail cache and transcode cache.
	DataPath string

	// ProtectHost is the UniFi OS console hosting Protect, e.g. "10.0.6.2".
	ProtectHost string
	// ProtectAPIKey authenticates against the Protect Integration API.
	ProtectAPIKey string
	// ProtectInsecureSkipVerify allows self-signed certs on the local console.
	ProtectInsecureSkipVerify bool

	// AuthUser is the single shared login username.
	AuthUser string
	// AuthPasswordHash is a bcrypt hash of the login password.
	AuthPasswordHash string
	// SessionSecret signs session cookies (HMAC key).
	SessionSecret string

	// IndexInterval is how often the full NVR directory is rescanned.
	IndexInterval time.Duration
	// TranscodeCacheTTL is how long transcoded proxy files are kept.
	TranscodeCacheTTL time.Duration
}

func Load() (Config, error) {
	c := Config{
		ListenAddr:                getEnv("LISTEN_ADDR", ":8080"),
		NVRPath:                   getEnv("NVR_PATH", "/nvr"),
		DataPath:                  getEnv("DATA_PATH", "/data"),
		ProtectHost:               os.Getenv("PROTECT_HOST"),
		ProtectAPIKey:             os.Getenv("PROTECT_API_KEY"),
		ProtectInsecureSkipVerify: getEnvBool("PROTECT_INSECURE_SKIP_VERIFY", true),
		AuthUser:                  os.Getenv("AUTH_USER"),
		AuthPasswordHash:          os.Getenv("AUTH_PASSWORD_HASH"),
		SessionSecret:             os.Getenv("SESSION_SECRET"),
		IndexInterval:             getEnvDuration("INDEX_INTERVAL", 5*time.Minute),
		TranscodeCacheTTL:         getEnvDuration("TRANSCODE_CACHE_TTL", 14*24*time.Hour),
	}

	var missing []string
	if c.AuthUser == "" {
		missing = append(missing, "AUTH_USER")
	}
	if c.AuthPasswordHash == "" {
		missing = append(missing, "AUTH_PASSWORD_HASH")
	}
	if c.SessionSecret == "" {
		missing = append(missing, "SESSION_SECRET")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %v", missing)
	}

	return c, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

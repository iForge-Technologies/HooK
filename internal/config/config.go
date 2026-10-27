// Package config reads hub settings from environment variables.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Addr             string // listen address, e.g. ":8080"
	DataDir          string // holds hook.db and secret.key
	PublicURL        string // used for links in alerts, e.g. https://hook.example.com
	SecureCookies    bool   // set Secure on the session cookie
	TrustProxy       bool   // read client IP from X-Forwarded-For (behind Caddy)
	RawRetentionDays int    // raw heartbeats; hourly rollups are kept for 2 years
	Secret           string // base64 AES-256 key; generated into DataDir if empty
	AgentDir         string // agent binaries served to install.sh
	DeadmanURL       string // pinged every DeadmanEvery while the hub is healthy
	DeadmanFailURL   string // pinged instead while unhealthy (optional)
	DeadmanEvery     int    // seconds
}

func Load() Config {
	c := Config{
		Addr:             env("HOOK_ADDR", ":8080"),
		DataDir:          env("HOOK_DATA", "./data"),
		PublicURL:        strings.TrimRight(env("HOOK_PUBLIC_URL", ""), "/"),
		TrustProxy:       envBool("HOOK_TRUST_PROXY", false),
		RawRetentionDays: envInt("HOOK_RAW_RETENTION_DAYS", 30),
		Secret:           os.Getenv("HOOK_SECRET"),
	}
	c.AgentDir = env("HOOK_AGENT_DIR", filepath.Join(c.DataDir, "agents"))
	c.DeadmanURL = os.Getenv("HOOK_DEADMAN_URL")
	c.DeadmanFailURL = os.Getenv("HOOK_DEADMAN_FAIL_URL")
	c.DeadmanEvery = envInt("HOOK_DEADMAN_EVERY", 60)
	c.SecureCookies = envBool("HOOK_SECURE_COOKIES", strings.HasPrefix(c.PublicURL, "https://"))
	return c
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

func envBool(k string, def bool) bool {
	if b, err := strconv.ParseBool(os.Getenv(k)); err == nil {
		return b
	}
	return def
}

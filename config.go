package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration, loaded from environment variables so
// the service is 12-factor friendly and easy to drive from Docker / compose.
type Config struct {
	// new-api connection
	BaseURL     string // e.g. https://api.example.com (no trailing slash)
	AccessToken string // admin system access token
	UserID      string // admin user id, sent as New-Api-User header
	ChannelID   int    // target single-key channel id

	// behaviour
	DataDir      string        // directory holding the persisted key pool (pool.json)
	PollInterval time.Duration // how often to check channel status
	HTTPTimeout  time.Duration // per-request timeout against new-api
	Insecure     bool          // skip TLS verification (self-signed certs)

	// embedded web console
	WebListen   string // listen address, e.g. :8080
	WebUsername string // basic-auth username for the console
	WebPassword string // basic-auth password; empty disables auth (with a loud warning)
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// LoadConfig reads configuration from the environment and validates required fields.
func LoadConfig() (*Config, error) {
	c := &Config{
		BaseURL:     strings.TrimRight(getenv("NEWAPI_BASE_URL", ""), "/"),
		AccessToken: getenv("NEWAPI_ACCESS_TOKEN", ""),
		UserID:      getenv("NEWAPI_USER_ID", ""),
		DataDir:     getenv("DATA_DIR", "/data"),
		WebListen:   getenv("WEB_LISTEN", ":8080"),
		WebUsername: getenv("WEB_USERNAME", "admin"),
		WebPassword: getenv("WEB_PASSWORD", ""),
		Insecure:    strings.EqualFold(getenv("INSECURE_SKIP_VERIFY", "false"), "true"),
	}

	var missing []string
	if c.BaseURL == "" {
		missing = append(missing, "NEWAPI_BASE_URL")
	}
	if c.AccessToken == "" {
		missing = append(missing, "NEWAPI_ACCESS_TOKEN")
	}
	if c.UserID == "" {
		missing = append(missing, "NEWAPI_USER_ID")
	}
	channelRaw := getenv("CHANNEL_ID", "")
	if channelRaw == "" {
		missing = append(missing, "CHANNEL_ID")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	id, err := strconv.Atoi(channelRaw)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("CHANNEL_ID must be a positive integer, got %q", channelRaw)
	}
	c.ChannelID = id

	if c.PollInterval, err = parseDuration("POLL_INTERVAL", "60s"); err != nil {
		return nil, err
	}
	if c.PollInterval < time.Second {
		return nil, fmt.Errorf("POLL_INTERVAL must be at least 1s")
	}
	if c.HTTPTimeout, err = parseDuration("HTTP_TIMEOUT", "15s"); err != nil {
		return nil, err
	}

	return c, nil
}

func parseDuration(key, def string) (time.Duration, error) {
	raw := getenv(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid duration (e.g. 60s, 2m): %q", key, raw)
	}
	return d, nil
}

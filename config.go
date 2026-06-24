package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type InstanceConfig struct {
	BaseURL     string
	AccessToken string
	UserID      string
	ChannelID   int
	Insecure    bool
}

type Config struct {
	Instances    []*InstanceConfig
	DataDir      string
	PollInterval time.Duration
	HTTPTimeout  time.Duration
	WebListen    string
	WebUsername  string
	WebPassword  string
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func LoadConfig() (*Config, error) {
	c := &Config{
		DataDir:     getenv("DATA_DIR", "/data"),
		WebListen:   getenv("WEB_LISTEN", ":8080"),
		WebUsername: getenv("WEB_USERNAME", "admin"),
		WebPassword: getenv("WEB_PASSWORD", ""),
	}

	var err error
	if c.PollInterval, err = parseDuration("POLL_INTERVAL", "60s"); err != nil {
		return nil, err
	}
	if c.PollInterval < time.Second {
		return nil, fmt.Errorf("POLL_INTERVAL must be at least 1s")
	}
	if c.HTTPTimeout, err = parseDuration("HTTP_TIMEOUT", "15s"); err != nil {
		return nil, err
	}

	inst0, err := loadInstanceFromEnv("NEWAPI_BASE_URL", "NEWAPI_ACCESS_TOKEN", "NEWAPI_USER_ID", "CHANNEL_ID", "INSECURE_SKIP_VERIFY")
	if err != nil {
		return nil, err
	}
	c.Instances = append(c.Instances, inst0)

	for n := 2; ; n++ {
		p := fmt.Sprintf("INSTANCE_%d_", n)
		if strings.TrimSpace(os.Getenv(p+"BASE_URL")) == "" {
			break
		}
		inst, err := loadInstanceFromEnv(p+"BASE_URL", p+"ACCESS_TOKEN", p+"USER_ID", p+"CHANNEL_ID", p+"INSECURE_SKIP_VERIFY")
		if err != nil {
			return nil, fmt.Errorf("instance %d: %w", n, err)
		}
		c.Instances = append(c.Instances, inst)
	}

	return c, nil
}

func loadInstanceFromEnv(baseURLKey, tokenKey, userIDKey, channelKey, insecureKey string) (*InstanceConfig, error) {
	inst := &InstanceConfig{
		BaseURL:     strings.TrimRight(strings.TrimSpace(os.Getenv(baseURLKey)), "/"),
		AccessToken: strings.TrimSpace(os.Getenv(tokenKey)),
		UserID:      strings.TrimSpace(os.Getenv(userIDKey)),
		Insecure:    strings.EqualFold(strings.TrimSpace(os.Getenv(insecureKey)), "true"),
	}

	var missing []string
	if inst.BaseURL == "" {
		missing = append(missing, baseURLKey)
	}
	if inst.AccessToken == "" {
		missing = append(missing, tokenKey)
	}
	if inst.UserID == "" {
		missing = append(missing, userIDKey)
	}
	channelRaw := strings.TrimSpace(os.Getenv(channelKey))
	if channelRaw == "" {
		missing = append(missing, channelKey)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	id, err := strconv.Atoi(channelRaw)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("%s must be a positive integer, got %q", channelKey, channelRaw)
	}
	inst.ChannelID = id
	return inst, nil
}

func parseDuration(key, def string) (time.Duration, error) {
	raw := getenv(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid duration (e.g. 60s, 2m): %q", key, raw)
	}
	return d, nil
}

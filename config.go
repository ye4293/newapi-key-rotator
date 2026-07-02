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
	ChannelIDs  []int
	Platform    string
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

	inst0, err := loadInstanceFromEnv(
		"NEWAPI_BASE_URL", "NEWAPI_ACCESS_TOKEN", "NEWAPI_USER_ID",
		"CHANNEL_ID", "CHANNEL_IDS", "NEWAPI_PLATFORM", "INSECURE_SKIP_VERIFY",
	)
	if err != nil {
		return nil, err
	}
	c.Instances = append(c.Instances, inst0)

	for n := 2; ; n++ {
		p := fmt.Sprintf("INSTANCE_%d_", n)
		if strings.TrimSpace(os.Getenv(p+"BASE_URL")) == "" {
			break
		}
		inst, err := loadInstanceFromEnv(
			p+"BASE_URL", p+"ACCESS_TOKEN", p+"USER_ID",
			p+"CHANNEL_ID", p+"CHANNEL_IDS", p+"PLATFORM", p+"INSECURE_SKIP_VERIFY",
		)
		if err != nil {
			return nil, fmt.Errorf("instance %d: %w", n, err)
		}
		c.Instances = append(c.Instances, inst)
	}

	return c, nil
}

func loadInstanceFromEnv(baseURLKey, tokenKey, userIDKey, channelKey, channelIDsKey, platformKey, insecureKey string) (*InstanceConfig, error) {
	inst := &InstanceConfig{
		BaseURL:     strings.TrimRight(strings.TrimSpace(os.Getenv(baseURLKey)), "/"),
		AccessToken: strings.TrimSpace(os.Getenv(tokenKey)),
		UserID:      strings.TrimSpace(os.Getenv(userIDKey)),
		Platform:    strings.TrimSpace(os.Getenv(platformKey)),
		Insecure:    strings.EqualFold(strings.TrimSpace(os.Getenv(insecureKey)), "true"),
	}

	var missing []string
	if inst.BaseURL == "" {
		missing = append(missing, baseURLKey)
	}
	if inst.AccessToken == "" {
		missing = append(missing, tokenKey)
	}
	// UserID 为可选字段，不再加入 missing 列表

	// 优先使用 CHANNEL_IDS（逗号分隔多个渠道 ID），回退到 CHANNEL_ID（单个，向后兼容）
	channelIDsRaw := strings.TrimSpace(os.Getenv(channelIDsKey))
	channelRaw := strings.TrimSpace(os.Getenv(channelKey))

	if channelIDsRaw != "" {
		// 解析逗号分隔的多个渠道 ID
		parts := strings.Split(channelIDsRaw, ",")
		seen := make(map[int]bool)
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.Atoi(part)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("%s contains invalid channel ID %q: must be a positive integer", channelIDsKey, part)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			inst.ChannelIDs = append(inst.ChannelIDs, id)
		}
		if len(inst.ChannelIDs) == 0 {
			return nil, fmt.Errorf("%s is set but contains no valid channel IDs", channelIDsKey)
		}
	} else if channelRaw != "" {
		// 向后兼容：单个渠道 ID
		id, err := strconv.Atoi(channelRaw)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%s must be a positive integer, got %q", channelKey, channelRaw)
		}
		inst.ChannelIDs = []int{id}
	} else {
		// 两者均未配置
		missing = append(missing, channelKey)
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

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

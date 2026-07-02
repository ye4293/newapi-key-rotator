package main

import (
	"os"
	"testing"
)

func TestLoadConfig_ChannelIDs_single(t *testing.T) {
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Setenv("CHANNEL_ID", "42")
	os.Unsetenv("NEWAPI_USER_ID")
	os.Unsetenv("CHANNEL_IDS")
	defer func() {
		os.Unsetenv("NEWAPI_BASE_URL")
		os.Unsetenv("NEWAPI_ACCESS_TOKEN")
		os.Unsetenv("CHANNEL_ID")
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("want 1 instance, got %d", len(cfg.Instances))
	}
	inst := cfg.Instances[0]
	if len(inst.ChannelIDs) != 1 || inst.ChannelIDs[0] != 42 {
		t.Errorf("want ChannelIDs=[42], got %v", inst.ChannelIDs)
	}
	if inst.UserID != "" {
		t.Errorf("want empty UserID, got %q", inst.UserID)
	}
}

func TestLoadConfig_ChannelIDs_multi(t *testing.T) {
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Setenv("CHANNEL_IDS", "10, 20 , 30")
	os.Unsetenv("CHANNEL_ID")
	defer func() {
		os.Unsetenv("NEWAPI_BASE_URL")
		os.Unsetenv("NEWAPI_ACCESS_TOKEN")
		os.Unsetenv("CHANNEL_IDS")
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inst := cfg.Instances[0]
	want := []int{10, 20, 30}
	if len(inst.ChannelIDs) != len(want) {
		t.Fatalf("want %v, got %v", want, inst.ChannelIDs)
	}
	for i, v := range want {
		if inst.ChannelIDs[i] != v {
			t.Errorf("index %d: want %d, got %d", i, v, inst.ChannelIDs[i])
		}
	}
}

func TestLoadConfig_Platform(t *testing.T) {
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Setenv("CHANNEL_ID", "1")
	os.Setenv("NEWAPI_PLATFORM", "ezlinkai")
	defer func() {
		os.Unsetenv("NEWAPI_BASE_URL")
		os.Unsetenv("NEWAPI_ACCESS_TOKEN")
		os.Unsetenv("CHANNEL_ID")
		os.Unsetenv("NEWAPI_PLATFORM")
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Instances[0].Platform != "ezlinkai" {
		t.Errorf("want platform=ezlinkai, got %q", cfg.Instances[0].Platform)
	}
}

func TestLoadConfig_MissingChannelID_error(t *testing.T) {
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Unsetenv("CHANNEL_ID")
	os.Unsetenv("CHANNEL_IDS")
	defer func() {
		os.Unsetenv("NEWAPI_BASE_URL")
		os.Unsetenv("NEWAPI_ACCESS_TOKEN")
	}()

	_, err := LoadConfig()
	if err == nil {
		t.Error("want error when no channel ID configured")
	}
}

func TestLoadConfig_ChannelIDs_invalid(t *testing.T) {
	cases := []struct {
		name       string
		channelIDs string
		wantErr    bool
	}{
		{"zero", "0", true},
		{"negative", "-1", true},
		{"non-numeric", "abc", true},
		{"all-empty-segments", ",,", true},
		{"mixed valid and invalid", "10,abc", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Setenv("NEWAPI_BASE_URL", "https://example.com")
			os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
			os.Setenv("CHANNEL_IDS", tc.channelIDs)
			os.Unsetenv("CHANNEL_ID")
			defer func() {
				os.Unsetenv("NEWAPI_BASE_URL")
				os.Unsetenv("NEWAPI_ACCESS_TOKEN")
				os.Unsetenv("CHANNEL_IDS")
			}()
			_, err := LoadConfig()
			if tc.wantErr && err == nil {
				t.Errorf("want error for CHANNEL_IDS=%q, got nil", tc.channelIDs)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("want no error for CHANNEL_IDS=%q, got %v", tc.channelIDs, err)
			}
		})
	}
}

func TestLoadConfig_ChannelIDs_dedup(t *testing.T) {
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Setenv("CHANNEL_IDS", "10,20,10,30")
	os.Unsetenv("CHANNEL_ID")
	defer func() {
		os.Unsetenv("NEWAPI_BASE_URL")
		os.Unsetenv("NEWAPI_ACCESS_TOKEN")
		os.Unsetenv("CHANNEL_IDS")
	}()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := cfg.Instances[0].ChannelIDs
	want := []int{10, 20, 30}
	if len(got) != len(want) {
		t.Fatalf("want %v after dedup, got %v", want, got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("index %d: want %d, got %d", i, v, got[i])
		}
	}
}

func TestLoadConfig_ChannelIDs_priority(t *testing.T) {
	// CHANNEL_IDS 应优先于 CHANNEL_ID
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Setenv("CHANNEL_IDS", "5,6")
	os.Setenv("CHANNEL_ID", "99") // 应被忽略
	defer func() {
		os.Unsetenv("NEWAPI_BASE_URL")
		os.Unsetenv("NEWAPI_ACCESS_TOKEN")
		os.Unsetenv("CHANNEL_IDS")
		os.Unsetenv("CHANNEL_ID")
	}()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := cfg.Instances[0].ChannelIDs
	if len(got) != 2 || got[0] != 5 || got[1] != 6 {
		t.Errorf("CHANNEL_IDS should take priority, want [5 6], got %v", got)
	}
}

func TestLoadConfig_Accounts(t *testing.T) {
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Setenv("CHANNEL_ID", "1")
	os.Setenv("ACCOUNT_1_PASSWORD", "pw-a")
	os.Setenv("ACCOUNT_1_CHANNELS", "0:42,0:88")
	os.Setenv("ACCOUNT_1_LABEL", "供货商A")
	os.Setenv("ACCOUNT_2_PASSWORD", "pw-b")
	os.Setenv("ACCOUNT_2_CHANNELS", "1:15")
	os.Unsetenv("ACCOUNT_2_LABEL")
	defer func() {
		for _, k := range []string{
			"NEWAPI_BASE_URL", "NEWAPI_ACCESS_TOKEN", "CHANNEL_ID",
			"ACCOUNT_1_PASSWORD", "ACCOUNT_1_CHANNELS", "ACCOUNT_1_LABEL",
			"ACCOUNT_2_PASSWORD", "ACCOUNT_2_CHANNELS",
		} {
			os.Unsetenv(k)
		}
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Accounts) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(cfg.Accounts))
	}
	a := cfg.Accounts[0]
	if a.Password != "pw-a" || a.Label != "供货商A" {
		t.Errorf("account 0: got password=%q label=%q", a.Password, a.Label)
	}
	if len(a.Channels) != 2 {
		t.Fatalf("account 0: want 2 channels, got %d", len(a.Channels))
	}
	if a.Channels[0].InstIdx != 0 || a.Channels[0].ChannelID != 42 {
		t.Errorf("account 0 channel 0: got %+v", a.Channels[0])
	}
	if a.Channels[1].InstIdx != 0 || a.Channels[1].ChannelID != 88 {
		t.Errorf("account 0 channel 1: got %+v", a.Channels[1])
	}
	b := cfg.Accounts[1]
	if b.Label != "" {
		t.Errorf("account 1: want empty label, got %q", b.Label)
	}
	if len(b.Channels) != 1 || b.Channels[0].InstIdx != 1 || b.Channels[0].ChannelID != 15 {
		t.Errorf("account 1 channels: got %+v", b.Channels)
	}
}

func TestLoadConfig_Accounts_empty(t *testing.T) {
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Setenv("CHANNEL_ID", "1")
	os.Unsetenv("ACCOUNT_1_PASSWORD")
	defer func() {
		os.Unsetenv("NEWAPI_BASE_URL")
		os.Unsetenv("NEWAPI_ACCESS_TOKEN")
		os.Unsetenv("CHANNEL_ID")
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Accounts) != 0 {
		t.Errorf("want 0 accounts when none configured, got %d", len(cfg.Accounts))
	}
}

func TestLoadConfig_Accounts_invalidChannels(t *testing.T) {
	os.Setenv("NEWAPI_BASE_URL", "https://example.com")
	os.Setenv("NEWAPI_ACCESS_TOKEN", "tok")
	os.Setenv("CHANNEL_ID", "1")
	os.Setenv("ACCOUNT_1_PASSWORD", "pw")
	os.Setenv("ACCOUNT_1_CHANNELS", "bad-format")
	defer func() {
		os.Unsetenv("NEWAPI_BASE_URL")
		os.Unsetenv("NEWAPI_ACCESS_TOKEN")
		os.Unsetenv("CHANNEL_ID")
		os.Unsetenv("ACCOUNT_1_PASSWORD")
		os.Unsetenv("ACCOUNT_1_CHANNELS")
	}()

	_, err := LoadConfig()
	if err == nil {
		t.Error("want error for invalid ACCOUNT_1_CHANNELS format")
	}
}

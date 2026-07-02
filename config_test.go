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

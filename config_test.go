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

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// new-api channel status codes (see common/constants.go in the new-api source).
const (
	channelStatusEnabled          = 1
	channelStatusManuallyDisabled = 2
	channelStatusAutoDisabled     = 3
)

// Client talks to the new-api admin HTTP API as an admin user.
type Client struct {
	baseURL string
	token   string
	userID  string
	http    *http.Client
}

// apiResp is the standard new-api response envelope. Most failures return HTTP 200
// with success=false, so callers must check Success rather than the status code alone.
type apiResp struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func NewClient(inst *InstanceConfig, cfg *Config) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if inst.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		baseURL: inst.BaseURL,
		token:   inst.AccessToken,
		userID:  inst.UserID,
		http:    &http.Client{Timeout: cfg.HTTPTimeout, Transport: transport},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*apiResp, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("New-Api-User", c.userID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("unauthorized (HTTP 401) — check NEWAPI_ACCESS_TOKEN / NEWAPI_USER_ID and that the user is an admin")
	}

	var out apiResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unexpected response (HTTP %d): %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if !out.Success {
		msg := out.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("new-api error: %s", msg)
	}
	return &out, nil
}

// GetChannel fetches a channel and returns its status plus the full object (as a
// generic map) so the caller can round-trip every field on update.
func (c *Client) GetChannel(ctx context.Context, id int) (status int, channel map[string]any, err error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/channel/%d", id), nil)
	if err != nil {
		return 0, nil, err
	}
	channel = make(map[string]any)
	if err := json.Unmarshal(resp.Data, &channel); err != nil {
		return 0, nil, fmt.Errorf("parse channel data: %w", err)
	}
	s, ok := channel["status"].(float64)
	if !ok {
		return 0, nil, fmt.Errorf("channel response missing numeric status field")
	}
	return int(s), channel, nil
}

// ReEnableChannel sets the channel status back to enabled without changing the key.
// Used to test whether an auto-disable was transient before committing to key rotation.
func (c *Client) ReEnableChannel(ctx context.Context, channel map[string]any) error {
	channel["status"] = channelStatusEnabled
	delete(channel, "channel_info")
	body, err := json.Marshal(channel)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPut, "/api/channel/", body)
	return err
}

// ApplyKeyAndEnable replaces the channel key and re-enables it via a read-modify-write
// PUT, reusing the channel object returned by GetChannel. new-api treats a non-empty
// key on a single-key channel as a replacement and re-runs abilities + cache refresh.
func (c *Client) ApplyKeyAndEnable(ctx context.Context, channel map[string]any, newKey string) error {
	channel["key"] = newKey
	channel["status"] = channelStatusEnabled
	// channel_info is overwritten server-side with the original; drop it to keep the
	// payload lean and avoid sending stale nested state.
	delete(channel, "channel_info")

	body, err := json.Marshal(channel)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPut, "/api/channel/", body)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

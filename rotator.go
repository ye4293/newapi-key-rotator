package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type Rotator struct {
	label     string // 日志标识，如 "ezlinkai/ch-42"
	channelID int    // 该 Rotator 实际监控的渠道 ID
	instCfg   *InstanceConfig
	cfg      *Config
	client   *Client
	store    *Store

	mu               sync.Mutex
	paused           bool
	lastStatus       int
	lastAction       string
	lastError        string
	lastChecked      time.Time
	pendingRotation  bool
	warnedEmpty      bool
	channelUsedQuota int64
	channelBalance   float64
}

func NewRotator(label string, channelID int, instCfg *InstanceConfig, cfg *Config, client *Client, store *Store) *Rotator {
	return &Rotator{
		label:     label,
		channelID: channelID,
		instCfg:   instCfg,
		cfg:       cfg,
		client:    client,
		store:     store,
		paused:    store.GetPaused(),
	}
}

func (r *Rotator) Pause() {
	r.mu.Lock()
	r.paused = true
	r.mu.Unlock()
	_ = r.store.SetPaused(true)
}

func (r *Rotator) Resume() {
	r.mu.Lock()
	r.paused = false
	r.mu.Unlock()
	_ = r.store.SetPaused(false)
}

func (r *Rotator) Run(ctx context.Context, trigger <-chan struct{}) {
	r.tick(ctx)
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		case <-trigger:
			r.tick(ctx)
		}
	}
}

func (r *Rotator) tick(ctx context.Context) {
	r.mu.Lock()
	paused := r.paused
	r.mu.Unlock()
	if paused {
		return
	}
	chID := r.store.ChannelID(r.channelID)
	status, channel, err := r.client.GetChannel(ctx, chID)
	if err != nil {
		r.recordError("get channel: " + err.Error())
		log.Printf("ERROR [%s] check: %v", r.label, err)
		return
	}
	r.recordStatus(status, channel)

	if status != channelStatusAutoDisabled {
		r.mu.Lock()
		if r.pendingRotation {
			r.pendingRotation = false
			r.mu.Unlock()
			log.Printf("INFO [%s] recovered after re-enable — key is still valid, no rotation", r.label)
		} else {
			r.mu.Unlock()
		}
		return
	}

	// Channel is auto-disabled.
	r.mu.Lock()
	pending := r.pendingRotation
	r.mu.Unlock()

	if !pending {
		// First time seeing auto-disable: re-enable with the same key before rotating.
		if err := r.client.ReEnableChannel(ctx, channel); err != nil {
			r.recordError("re-enable: " + err.Error())
			log.Printf("ERROR [%s] re-enable with same key: %v", r.label, err)
			return
		}
		r.mu.Lock()
		r.pendingRotation = true
		r.mu.Unlock()
		r.recordAction("auto-disabled → re-enabled same key (will rotate if disabled again)")
		log.Printf("INFO [%s] auto-disabled → re-enabled same key, watching next cycle", r.label)
		return
	}

	// Still auto-disabled after re-enable attempt: key is genuinely bad, rotate.
	r.mu.Lock()
	r.pendingRotation = false
	r.mu.Unlock()

	next, idx, ok := r.store.PeekNext()
	if !ok {
		r.recordAction("pool exhausted — channel left auto-disabled")
		r.mu.Lock()
		warned := r.warnedEmpty
		r.warnedEmpty = true
		r.mu.Unlock()
		if !warned {
			log.Printf("WARN [%s] auto-disabled but key pool is empty/exhausted; not rotating", r.label)
		}
		return
	}

	if err := r.client.ApplyKeyAndEnable(ctx, channel, next); err != nil {
		r.recordError("apply key: " + err.Error())
		log.Printf("ERROR [%s] apply key #%d: %v", r.label, idx+1, err)
		return
	}
	if err := r.store.CommitAdvance(); err != nil {
		log.Printf("ERROR [%s] persist progress after applying key #%d: %v", r.label, idx+1, err)
	}

	total := r.store.Snapshot().Total
	r.recordAction(fmt.Sprintf("rotated to key %d/%d (%s) and re-enabled", idx+1, total, maskKey(next)))
	r.mu.Lock()
	r.warnedEmpty = false
	r.mu.Unlock()
	log.Printf("INFO [%s] auto-disabled again → applied key %d/%d (%s) and re-enabled", r.label, idx+1, total, maskKey(next))
}

type Status struct {
	ChannelID        int          `json:"channel_id"`
	DefaultChannelID int          `json:"default_channel_id"`
	IsCustomChannel  bool         `json:"is_custom_channel"`
	Paused           bool         `json:"paused"`
	LastStatus       int          `json:"last_status"`
	PendingRotation  bool         `json:"pending_rotation"`
	LastAction       string       `json:"last_action"`
	LastError        string       `json:"last_error"`
	LastChecked      string       `json:"last_checked"`
	ChannelUsedQuota int64        `json:"channel_used_quota"`
	ChannelBalance   float64      `json:"channel_balance"`
	Pool             PoolSnapshot `json:"pool"`
}

func (r *Rotator) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	checked := ""
	if !r.lastChecked.IsZero() {
		checked = r.lastChecked.Format(time.RFC3339)
	}
	effective := r.store.ChannelID(r.channelID)
	return Status{
		ChannelID:        effective,
		DefaultChannelID: r.channelID,
		IsCustomChannel:  effective != r.channelID,
		Paused:           r.paused,
		LastStatus:       r.lastStatus,
		PendingRotation:  r.pendingRotation,
		LastAction:       r.lastAction,
		LastError:        r.lastError,
		LastChecked:      checked,
		ChannelUsedQuota: r.channelUsedQuota,
		ChannelBalance:   r.channelBalance,
		Pool:             r.store.Snapshot(),
	}
}

func (r *Rotator) recordStatus(status int, channel map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastStatus = status
	r.lastChecked = time.Now()
	r.lastError = ""
	if q, ok := channel["used_quota"].(float64); ok {
		r.channelUsedQuota = int64(q)
	}
	if b, ok := channel["balance"].(float64); ok {
		r.channelBalance = b
	}
}

func (r *Rotator) recordAction(action string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastAction = action
}

func (r *Rotator) recordError(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastError = msg
	r.lastChecked = time.Now()
}

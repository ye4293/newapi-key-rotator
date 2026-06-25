package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type Rotator struct {
	instCfg  *InstanceConfig
	cfg      *Config
	client   *Client
	store    *Store
	onRotate func(newIndex int)

	mu               sync.Mutex
	lastStatus       int
	lastAction       string
	lastError        string
	lastChecked      time.Time
	pendingRotation  bool  // true after first re-enable attempt; auto-disable again means real rotation
	warnedEmpty      bool
	channelUsedQuota int64
	channelBalance   float64
}

func NewRotator(instCfg *InstanceConfig, cfg *Config, client *Client, store *Store) *Rotator {
	return &Rotator{instCfg: instCfg, cfg: cfg, client: client, store: store}
}

// SetOnRotate registers a callback invoked (in a goroutine) after each successful key rotation.
// newIndex is the store index after CommitAdvance — the applied key was at newIndex-1.
func (r *Rotator) SetOnRotate(fn func(newIndex int)) {
	r.onRotate = fn
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
	chID := r.store.ChannelID(r.instCfg.ChannelID)
	status, channel, err := r.client.GetChannel(ctx, chID)
	if err != nil {
		r.recordError("get channel: " + err.Error())
		log.Printf("ERROR check channel #%d: %v", chID, err)
		return
	}
	r.recordStatus(status, channel)

	if status != channelStatusAutoDisabled {
		r.mu.Lock()
		if r.pendingRotation {
			r.pendingRotation = false
			r.mu.Unlock()
			log.Printf("INFO channel #%d recovered after re-enable — key is still valid, no rotation", chID)
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
			log.Printf("ERROR channel #%d re-enable with same key: %v", chID, err)
			return
		}
		r.mu.Lock()
		r.pendingRotation = true
		r.mu.Unlock()
		r.recordAction("auto-disabled → re-enabled same key (will rotate if disabled again)")
		log.Printf("INFO channel #%d auto-disabled → re-enabled same key, watching next cycle", chID)
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
			log.Printf("WARN channel #%d auto-disabled but key pool is empty/exhausted; not rotating", chID)
		}
		return
	}

	if err := r.client.ApplyKeyAndEnable(ctx, channel, next); err != nil {
		r.recordError("apply key: " + err.Error())
		log.Printf("ERROR channel #%d apply key #%d: %v", chID, idx+1, err)
		return
	}
	if err := r.store.CommitAdvance(); err != nil {
		log.Printf("ERROR persist progress after applying key #%d: %v", idx+1, err)
	}

	total := r.store.Snapshot().Total
	r.recordAction(fmt.Sprintf("rotated to key %d/%d (%s) and re-enabled", idx+1, total, maskKey(next)))
	r.mu.Lock()
	r.warnedEmpty = false
	r.mu.Unlock()
	log.Printf("INFO channel #%d auto-disabled again → applied key %d/%d (%s) and re-enabled", chID, idx+1, total, maskKey(next))

	if r.onRotate != nil {
		newIdx := r.store.Snapshot().Index
		go r.onRotate(newIdx)
	}
}

// SyncToIndex applies the key at newIndex-1 to this instance's channel and advances
// the local pool to newIndex. Called when a peer instance just rotated.
func (r *Rotator) SyncToIndex(ctx context.Context, newIndex int) {
	key, ok := r.store.KeyAt(newIndex - 1)
	if !ok {
		log.Printf("WARN sync: key at index %d not found in pool (size %d)", newIndex-1, r.store.Snapshot().Total)
		return
	}
	chID := r.store.ChannelID(r.instCfg.ChannelID)
	_, channel, err := r.client.GetChannel(ctx, chID)
	if err != nil {
		log.Printf("ERROR sync channel #%d: %v", chID, err)
		return
	}
	if err := r.client.ApplyKeyAndEnable(ctx, channel, key); err != nil {
		log.Printf("ERROR sync channel #%d apply key: %v", chID, err)
		return
	}
	if err := r.store.SyncToIndex(newIndex); err != nil {
		log.Printf("ERROR sync pool index to %d: %v", newIndex, err)
		return
	}
	log.Printf("INFO channel #%d synced to key %d/%d (%s)", chID, newIndex, r.store.Snapshot().Total, maskKey(key))
}

type Status struct {
	ChannelID        int          `json:"channel_id"`
	DefaultChannelID int          `json:"default_channel_id"`
	IsCustomChannel  bool         `json:"is_custom_channel"`
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
	effective := r.store.ChannelID(r.instCfg.ChannelID)
	return Status{
		ChannelID:        effective,
		DefaultChannelID: r.instCfg.ChannelID,
		IsCustomChannel:  effective != r.instCfg.ChannelID,
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

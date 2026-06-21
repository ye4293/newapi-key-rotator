package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Rotator runs the failover loop: every tick it checks the target channel and, when
// new-api has auto-disabled it, swaps in the next backup key and re-enables it.
type Rotator struct {
	cfg    *Config
	client *Client
	store  *Store

	mu          sync.Mutex
	lastStatus  int
	lastAction  string
	lastError   string
	lastChecked time.Time
	warnedEmpty bool // avoid log spam once the pool is exhausted
}

func NewRotator(cfg *Config, client *Client, store *Store) *Rotator {
	return &Rotator{cfg: cfg, client: client, store: store}
}

// Run blocks until ctx is cancelled. It runs one tick immediately, then on every
// poll interval, and also whenever a manual trigger fires (e.g. after the console
// submits a fresh key batch) so changes take effect without waiting a full cycle.
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
	status, channel, err := r.client.GetChannel(ctx, r.cfg.ChannelID)
	if err != nil {
		r.recordError("get channel: " + err.Error())
		log.Printf("ERROR check channel #%d: %v", r.cfg.ChannelID, err)
		return
	}
	r.recordStatus(status)

	if status != channelStatusAutoDisabled {
		// Enabled or manually disabled — nothing to do. A manual disable is treated
		// as a deliberate operator action and left untouched.
		return
	}

	next, idx, ok := r.store.PeekNext()
	if !ok {
		r.recordAction("pool exhausted — channel left auto-disabled")
		r.mu.Lock()
		warned := r.warnedEmpty
		r.warnedEmpty = true
		r.mu.Unlock()
		if !warned {
			log.Printf("WARN channel #%d auto-disabled but key pool is empty/exhausted; not rotating", r.cfg.ChannelID)
		}
		return
	}

	if err := r.client.ApplyKeyAndEnable(ctx, channel, next); err != nil {
		r.recordError("apply key: " + err.Error())
		log.Printf("ERROR channel #%d apply key #%d: %v", r.cfg.ChannelID, idx+1, err)
		return
	}
	if err := r.store.CommitAdvance(); err != nil {
		// The upstream key was already swapped; only progress persistence failed.
		// Log loudly so a restart doesn't reuse the same key.
		log.Printf("ERROR persist progress after applying key #%d: %v", idx+1, err)
	}

	total := r.store.Snapshot().Total
	r.recordAction(fmt.Sprintf("rotated to key %d/%d (%s) and re-enabled", idx+1, total, maskKey(next)))
	r.mu.Lock()
	r.warnedEmpty = false
	r.mu.Unlock()
	log.Printf("INFO channel #%d auto-disabled → applied key %d/%d (%s) and re-enabled", r.cfg.ChannelID, idx+1, total, maskKey(next))
}

// Status is the live view surfaced by the web console.
type Status struct {
	ChannelID   int          `json:"channel_id"`
	LastStatus  int          `json:"last_status"`
	LastAction  string       `json:"last_action"`
	LastError   string       `json:"last_error"`
	LastChecked string       `json:"last_checked"`
	Pool        PoolSnapshot `json:"pool"`
}

func (r *Rotator) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	checked := ""
	if !r.lastChecked.IsZero() {
		checked = r.lastChecked.Format(time.RFC3339)
	}
	return Status{
		ChannelID:   r.cfg.ChannelID,
		LastStatus:  r.lastStatus,
		LastAction:  r.lastAction,
		LastError:   r.lastError,
		LastChecked: checked,
		Pool:        r.store.Snapshot(),
	}
}

func (r *Rotator) recordStatus(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastStatus = status
	r.lastChecked = time.Now()
	r.lastError = ""
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

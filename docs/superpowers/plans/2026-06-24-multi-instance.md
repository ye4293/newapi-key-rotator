# Multi-Instance Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow a single process to monitor and auto-rotate keys for multiple independent new-api instances, each with its own key pool.

**Architecture:** Introduce `InstanceConfig` for per-instance fields; `Config` keeps shared fields. `main.go` builds one `(Client, Store, Rotator, trigger)` tuple per instance and passes the slice to `Server`, which routes `/api/instance/{idx}/...` requests to the correct tuple. The web UI fetches `/api/instances` on load and renders a tab bar.

**Tech Stack:** Go 1.22 (`net/http` path params), plain HTML/CSS/JS (no framework), `.env` environment variables.

---

## File Map

| File | Change |
|------|--------|
| `config.go` | Full rewrite — add `InstanceConfig`, refactor `LoadConfig` to scan `INSTANCE_N_*` env vars |
| `store.go` | Change `NewStore(dataDir string)` → `NewStore(poolPath string)` |
| `client.go` | Change `NewClient(cfg *Config)` → `NewClient(inst *InstanceConfig, cfg *Config)` |
| `rotator.go` | Add `instCfg *InstanceConfig` field; replace all `r.cfg.ChannelID` with `r.instCfg.ChannelID` |
| `main.go` | Full rewrite — define `instance` struct, loop over `cfg.Instances`, pass slice to `NewServer` |
| `server.go` | Full rewrite — hold `[]*instance`, add scoped routes, keep legacy routes for instance 0 |
| `web/index.html` | Add tab bar; scope all API calls to `activeIdx` |
| `.env` | Append four `INSTANCE_2_*` lines |

---

## Task 1: Rewrite config.go

**Files:**
- Modify: `config.go`

- [ ] **Step 1: Replace config.go with the new multi-instance version**

```go
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
```

> Note: `go build` will fail until Task 3 (main.go) is complete — callers still reference the old `*Config` shape. This is expected.

---

## Task 2: Update store.go, client.go, rotator.go

**Files:**
- Modify: `store.go` (NewStore signature)
- Modify: `client.go` (NewClient signature)
- Modify: `rotator.go` (add instCfg field, replace ChannelID references)

- [ ] **Step 1: Update NewStore in store.go to accept a full pool path**

Replace only the `NewStore` function (everything else in store.go stays the same):

```go
func NewStore(poolPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(poolPath), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir %q: %w", filepath.Dir(poolPath), err)
	}
	s := &Store{path: poolPath}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read pool file: %w", err)
	}
	if err := json.Unmarshal(data, &s.st); err != nil {
		return nil, fmt.Errorf("parse pool file %q: %w", s.path, err)
	}
	return s, nil
}
```

Also add `"path/filepath"` to the import block in store.go.

- [ ] **Step 2: Update NewClient in client.go to accept *InstanceConfig**

Replace only the `NewClient` function:

```go
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
```

- [ ] **Step 3: Rewrite rotator.go to use *InstanceConfig**

Full file replacement:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type Rotator struct {
	instCfg *InstanceConfig
	cfg     *Config
	client  *Client
	store   *Store

	mu               sync.Mutex
	lastStatus       int
	lastAction       string
	lastError        string
	lastChecked      time.Time
	warnedEmpty      bool
	channelUsedQuota int64
	channelBalance   float64
}

func NewRotator(instCfg *InstanceConfig, cfg *Config, client *Client, store *Store) *Rotator {
	return &Rotator{instCfg: instCfg, cfg: cfg, client: client, store: store}
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
	status, channel, err := r.client.GetChannel(ctx, r.instCfg.ChannelID)
	if err != nil {
		r.recordError("get channel: " + err.Error())
		log.Printf("ERROR check channel #%d: %v", r.instCfg.ChannelID, err)
		return
	}
	r.recordStatus(status, channel)

	if status != channelStatusAutoDisabled {
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
			log.Printf("WARN channel #%d auto-disabled but key pool is empty/exhausted; not rotating", r.instCfg.ChannelID)
		}
		return
	}

	if err := r.client.ApplyKeyAndEnable(ctx, channel, next); err != nil {
		r.recordError("apply key: " + err.Error())
		log.Printf("ERROR channel #%d apply key #%d: %v", r.instCfg.ChannelID, idx+1, err)
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
	log.Printf("INFO channel #%d auto-disabled → applied key %d/%d (%s) and re-enabled", r.instCfg.ChannelID, idx+1, total, maskKey(next))
}

type Status struct {
	ChannelID        int          `json:"channel_id"`
	LastStatus       int          `json:"last_status"`
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
	return Status{
		ChannelID:        r.instCfg.ChannelID,
		LastStatus:       r.lastStatus,
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
```

---

## Task 3: Rewrite main.go — restores build

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Replace main.go**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type instance struct {
	cfg     *InstanceConfig
	store   *Store
	rotator *Rotator
	trigger chan struct{}
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	instances := make([]*instance, len(cfg.Instances))
	for i, instCfg := range cfg.Instances {
		poolFile := fmt.Sprintf("pool_%d.json", i)
		if i == 0 {
			poolFile = "pool.json"
		}
		store, err := NewStore(filepath.Join(cfg.DataDir, poolFile))
		if err != nil {
			log.Fatalf("store error (instance %d): %v", i, err)
		}
		client := NewClient(instCfg, cfg)
		trigger := make(chan struct{}, 1)
		rotator := NewRotator(instCfg, cfg, client, store)
		instances[i] = &instance{cfg: instCfg, store: store, rotator: rotator, trigger: trigger}
	}

	server := NewServer(cfg, instances)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	doneChs := make([]chan struct{}, len(instances))
	for i, inst := range instances {
		done := make(chan struct{})
		doneChs[i] = done
		go func(inst *instance, done chan struct{}) {
			defer close(done)
			inst.rotator.Run(ctx, inst.trigger)
		}(inst, done)
	}

	httpSrv := &http.Server{
		Addr:              cfg.WebListen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("INFO web console listening on %s (%d instance(s), poll %s)", cfg.WebListen, len(instances), cfg.PollInterval)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("web server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("INFO shutting down…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	for _, done := range doneChs {
		<-done
	}
	_ = os.Stdout.Sync()
}
```

- [ ] **Step 2: Verify build passes**

```bash
go build ./...
```

Expected: no output, exit code 0.

- [ ] **Step 3: Commit**

```bash
git add config.go store.go client.go rotator.go main.go
git commit -m "refactor: multi-instance config, store, client, rotator, main"
```

---

## Task 4: Rewrite server.go

**Files:**
- Modify: `server.go`

- [ ] **Step 1: Replace server.go**

```go
package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
)

//go:embed web/index.html
var indexHTML []byte

type Server struct {
	cfg       *Config
	instances []*instance
}

func NewServer(cfg *Config, instances []*instance) *Server {
	return &Server{cfg: cfg, instances: instances}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/instances", s.handleInstances)
	mux.HandleFunc("/api/instance/{idx}/status", s.handleInstanceStatus)
	mux.HandleFunc("/api/instance/{idx}/keys", s.handleInstanceKeys)
	mux.HandleFunc("/api/instance/{idx}/keys/append", s.handleInstanceKeysAppend)
	mux.HandleFunc("/api/instance/{idx}/rotate-now", s.handleInstanceRotateNow)
	// Legacy routes — delegate to instance 0 for backward compatibility.
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.instances[0].rotator.Status())
	})
	mux.HandleFunc("/api/keys", func(w http.ResponseWriter, r *http.Request) {
		s.keysHandler(w, r, s.instances[0])
	})
	mux.HandleFunc("/api/keys/append", func(w http.ResponseWriter, r *http.Request) {
		s.keysAppendHandler(w, r, s.instances[0])
	})
	mux.HandleFunc("/api/rotate-now", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fireInstance(s.instances[0].trigger)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	})
	return s.withAuth(mux)
}

func (s *Server) getInstance(r *http.Request) (*instance, bool) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil || idx < 0 || idx >= len(s.instances) {
		return nil, false
	}
	return s.instances[idx], true
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.cfg.WebPassword == "" {
		log.Printf("WARN WEB_PASSWORD is not set — the web console is UNPROTECTED; set it before exposing this service")
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.WebUsername)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.WebPassword)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="newapi-key-rotator"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	type instanceInfo struct {
		Index     int    `json:"index"`
		BaseURL   string `json:"base_url"`
		ChannelID int    `json:"channel_id"`
	}
	infos := make([]instanceInfo, len(s.instances))
	for i, inst := range s.instances {
		infos[i] = instanceInfo{Index: i, BaseURL: inst.cfg.BaseURL, ChannelID: inst.cfg.ChannelID}
	}
	writeJSON(w, http.StatusOK, infos)
}

func (s *Server) handleInstanceStatus(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, inst.rotator.Status())
}

func (s *Server) handleInstanceKeys(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.keysHandler(w, r, inst)
}

func (s *Server) handleInstanceKeysAppend(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.keysAppendHandler(w, r, inst)
}

func (s *Server) handleInstanceRotateNow(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fireInstance(inst.trigger)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) keysHandler(w http.ResponseWriter, r *http.Request, inst *instance) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Keys string `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "invalid JSON body"})
		return
	}
	count, err := inst.store.SetKeys(body.Keys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	log.Printf("INFO channel #%d key pool replaced: %d key(s), progress reset", inst.cfg.ChannelID, count)
	fireInstance(inst.trigger)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "count": count})
}

func (s *Server) keysAppendHandler(w http.ResponseWriter, r *http.Request, inst *instance) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Keys string `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "invalid JSON body"})
		return
	}
	added, err := inst.store.AppendKeys(body.Keys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	log.Printf("INFO channel #%d key pool appended: %d new key(s) added", inst.cfg.ChannelID, added)
	if added > 0 {
		fireInstance(inst.trigger)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "added": added})
}

func fireInstance(trigger chan<- struct{}) {
	select {
	case trigger <- struct{}{}:
	default:
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 2: Verify build still passes**

```bash
go build ./...
```

Expected: no output, exit code 0.

- [ ] **Step 3: Commit**

```bash
git add server.go
git commit -m "feat: multi-instance server routes with legacy fallback to instance 0"
```

---

## Task 5: Rewrite web/index.html

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Replace web/index.html**

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>new-api Key 轮换控制台</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 24px;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif;
    background: #0f172a; color: #e2e8f0; line-height: 1.5;
  }
  .wrap { max-width: 760px; margin: 0 auto; }
  h1 { font-size: 20px; margin: 0 0 16px; }
  .tabs { display: flex; gap: 8px; margin-bottom: 20px; flex-wrap: wrap; }
  .tab-btn {
    border: 1px solid #334155; border-radius: 8px; padding: 8px 16px;
    font-size: 14px; font-weight: 600; cursor: pointer;
    background: #1e293b; color: #94a3b8;
  }
  .tab-btn.active { background: #2563eb; color: #fff; border-color: #2563eb; }
  .card { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 20px; margin-bottom: 20px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 12px; }
  .stat { background: #0f172a; border-radius: 8px; padding: 12px; }
  .stat .label { font-size: 12px; color: #94a3b8; }
  .stat .value { font-size: 18px; font-weight: 600; margin-top: 4px; word-break: break-all; }
  .badge { display: inline-block; padding: 2px 10px; border-radius: 999px; font-size: 13px; font-weight: 600; }
  .b-on { background: #064e3b; color: #6ee7b7; }
  .b-auto { background: #7c2d12; color: #fdba74; }
  .b-manual { background: #334155; color: #cbd5e1; }
  .b-unknown { background: #334155; color: #94a3b8; }
  textarea {
    width: 100%; min-height: 200px; padding: 12px; border-radius: 8px;
    border: 1px solid #334155; background: #0f172a; color: #e2e8f0;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; resize: vertical;
  }
  .row { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; margin-top: 12px; }
  button {
    border: none; border-radius: 8px; padding: 10px 18px; font-size: 14px; font-weight: 600;
    cursor: pointer; background: #2563eb; color: #fff;
  }
  button.secondary { background: #334155; color: #e2e8f0; }
  button:disabled { opacity: .5; cursor: default; }
  .muted { color: #94a3b8; font-size: 13px; }
  .err { color: #fca5a5; }
  .ok { color: #6ee7b7; }
  .hint { font-size: 12px; color: #94a3b8; margin-top: 6px; }
</style>
</head>
<body>
<div class="wrap">
  <h1>new-api Key 轮换控制台</h1>
  <div class="tabs" id="tab-bar"></div>

  <div class="card">
    <div class="grid">
      <div class="stat"><div class="label">目标渠道</div><div class="value" id="s-channel">—</div></div>
      <div class="stat"><div class="label">当前状态</div><div class="value"><span id="s-status" class="badge b-unknown">—</span></div></div>
      <div class="stat"><div class="label">Key 进度</div><div class="value" id="s-progress">—</div></div>
      <div class="stat"><div class="label">剩余可用</div><div class="value" id="s-remaining">—</div></div>
      <div class="stat"><div class="label">已用额度</div><div class="value" id="s-used-quota">—</div></div>
      <div class="stat"><div class="label">当前使用 Key</div><div class="value" id="s-current">—</div></div>
      <div class="stat"><div class="label">最后检查</div><div class="value" id="s-checked">—</div></div>
    </div>
    <div class="row">
      <button class="secondary" id="btn-rotate" type="button">立即执行一次检查</button>
      <span class="muted" id="s-action"></span>
    </div>
    <div class="row"><span class="err" id="s-error"></span></div>
  </div>

  <div class="card">
    <h1 style="font-size:16px">提交备用 Key 池</h1>
    <p class="muted">每行一个 key。自动去除空行与重复。渠道被自动禁用时会从这里依次取下一个 key 换上并启用。</p>
    <textarea id="keys" placeholder="sk-xxxxxxxx&#10;sk-yyyyyyyy&#10;sk-zzzzzzzz"></textarea>
    <div class="row">
      <button id="btn-submit" type="button">整批替换</button>
      <button id="btn-append" type="button" class="secondary">追加到末尾</button>
      <span id="submit-msg"></span>
    </div>
    <p class="hint">整批替换：清空旧池并重置进度到第一个。追加：保留已有 key 和当前进度，仅新增不重复的 key。出于安全，控制台不会回显 key 明文，仅显示数量与末 4 位。</p>
  </div>
</div>

<script>
let instances = [];
let activeIdx = 0;

const statusBadge = (s) => {
  switch (s) {
    case 1: return ['启用', 'b-on'];
    case 2: return ['手动禁用', 'b-manual'];
    case 3: return ['自动禁用', 'b-auto'];
    default: return ['未知/未检查', 'b-unknown'];
  }
};

function formatQuota(q) {
  if (q == null) return '—';
  q = q / 500000;
  if (q >= 1e9) return (q / 1e9).toFixed(2) + ' B';
  if (q >= 1e6) return (q / 1e6).toFixed(2) + ' M';
  if (q >= 1e3) return (q / 1e3).toFixed(1) + ' K';
  return parseFloat(q.toFixed(2)).toString();
}

function renderTabs() {
  const bar = document.getElementById('tab-bar');
  bar.innerHTML = '';
  instances.forEach((inst, i) => {
    const btn = document.createElement('button');
    btn.className = 'tab-btn' + (i === activeIdx ? ' active' : '');
    btn.textContent = `实例${i} · #${inst.channel_id}`;
    btn.onclick = () => { activeIdx = i; renderTabs(); refresh(); };
    bar.appendChild(btn);
  });
}

async function refresh() {
  const idx = activeIdx;
  try {
    const r = await fetch(`api/instance/${idx}/status`, { headers: { 'Accept': 'application/json' } });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    const d = await r.json();
    document.getElementById('s-channel').textContent = '#' + d.channel_id;
    const [text, cls] = statusBadge(d.last_status);
    const badge = document.getElementById('s-status');
    badge.textContent = text;
    badge.className = 'badge ' + cls;
    const p = d.pool || {};
    document.getElementById('s-progress').textContent = (p.index || 0) + ' / ' + (p.total || 0) + (p.exhausted ? ' (已用尽)' : '');
    document.getElementById('s-remaining').textContent = (p.remaining || 0);
    document.getElementById('s-used-quota').textContent = d.channel_used_quota != null ? formatQuota(d.channel_used_quota) : '—';
    document.getElementById('s-current').textContent = p.current_key || '—';
    document.getElementById('s-checked').textContent = d.last_checked ? new Date(d.last_checked).toLocaleString() : '—';
    document.getElementById('s-action').textContent = d.last_action ? ('最近动作：' + d.last_action) : '';
    document.getElementById('s-error').textContent = d.last_error ? ('错误：' + d.last_error) : '';
  } catch (e) {
    document.getElementById('s-error').textContent = '无法获取状态：' + e.message;
  }
}

async function submitKeys() {
  const btn = document.getElementById('btn-submit');
  const msg = document.getElementById('submit-msg');
  const keys = document.getElementById('keys').value;
  btn.disabled = true; msg.textContent = '保存中…'; msg.className = 'muted';
  try {
    const r = await fetch(`api/instance/${activeIdx}/keys`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ keys })
    });
    const d = await r.json();
    if (!r.ok || !d.success) throw new Error(d.message || ('HTTP ' + r.status));
    msg.textContent = '已替换，共 ' + d.count + ' 个 key，进度已重置。';
    msg.className = 'ok';
    document.getElementById('keys').value = '';
    refresh();
  } catch (e) {
    msg.textContent = '保存失败：' + e.message;
    msg.className = 'err';
  } finally {
    btn.disabled = false;
  }
}

async function appendKeys() {
  const btn = document.getElementById('btn-append');
  const msg = document.getElementById('submit-msg');
  const keys = document.getElementById('keys').value;
  btn.disabled = true; msg.textContent = '追加中…'; msg.className = 'muted';
  try {
    const r = await fetch(`api/instance/${activeIdx}/keys/append`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ keys })
    });
    const d = await r.json();
    if (!r.ok || !d.success) throw new Error(d.message || ('HTTP ' + r.status));
    msg.textContent = '已追加 ' + d.added + ' 个新 key（重复已跳过）。';
    msg.className = 'ok';
    document.getElementById('keys').value = '';
    refresh();
  } catch (e) {
    msg.textContent = '追加失败：' + e.message;
    msg.className = 'err';
  } finally {
    btn.disabled = false;
  }
}

async function rotateNow() {
  const btn = document.getElementById('btn-rotate');
  btn.disabled = true;
  try { await fetch(`api/instance/${activeIdx}/rotate-now`, { method: 'POST' }); } catch (e) {}
  setTimeout(() => { refresh(); btn.disabled = false; }, 800);
}

document.getElementById('btn-submit').addEventListener('click', submitKeys);
document.getElementById('btn-append').addEventListener('click', appendKeys);
document.getElementById('btn-rotate').addEventListener('click', rotateNow);

(async () => {
  try {
    const r = await fetch('api/instances', { headers: { 'Accept': 'application/json' } });
    instances = await r.json();
  } catch (e) {
    instances = [{ index: 0, channel_id: '?' }];
  }
  renderTabs();
  refresh();
  setInterval(() => refresh(), 5000);
})();
</script>
</body>
</html>
```

- [ ] **Step 2: Commit**

```bash
git add web/index.html
git commit -m "feat: multi-instance tab UI in web console"
```

---

## Task 6: Update .env, rebuild, restart, and verify

**Files:**
- Modify: `.env`

- [ ] **Step 1: Append instance 2 vars to .env**

Add these four lines to the end of `.env`:

```
INSTANCE_2_BASE_URL=http://45.78.201.134:3000
INSTANCE_2_ACCESS_TOKEN=gy27Lmcp/d50w6L/wcd8qV3lwZl5sYE8
INSTANCE_2_USER_ID=1
INSTANCE_2_CHANNEL_ID=19
```

- [ ] **Step 2: Rebuild the binary**

```bash
go build -o newapi-key-rotator.exe .
```

Expected: no output, exit code 0.

- [ ] **Step 3: Kill the running process and restart with new binary**

```bash
pkill -f newapi-key-rotator.exe 2>/dev/null || true
sleep 1
export $(grep -v '^#' .env | xargs) && ./newapi-key-rotator.exe &
sleep 2
```

Expected log line: `INFO web console listening on :8080 (2 instance(s), poll 1m0s)`

- [ ] **Step 4: Verify /api/instances returns both**

```bash
curl -s -u admin:miss666 http://localhost:8080/api/instances
```

Expected output (JSON array with two entries):
```json
[{"index":0,"base_url":"http://172.96.142.109:6008","channel_id":10246},{"index":1,"base_url":"http://45.78.201.134:3000","channel_id":19}]
```

- [ ] **Step 5: Verify instance 0 status**

```bash
curl -s -u admin:miss666 http://localhost:8080/api/instance/0/status | grep channel_id
```

Expected: `"channel_id":10246`

- [ ] **Step 6: Verify instance 1 status**

```bash
curl -s -u admin:miss666 http://localhost:8080/api/instance/1/status | grep channel_id
```

Expected: `"channel_id":19`

- [ ] **Step 7: Verify legacy route still works**

```bash
curl -s -u admin:miss666 http://localhost:8080/api/status | grep channel_id
```

Expected: `"channel_id":10246`

- [ ] **Step 8: Commit**

```bash
git add .env newapi-key-rotator.exe
git commit -m "feat: add second new-api instance (45.78.201.134:3000 channel #19)"
```

# Multi-Channel + Supplier Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 扩展 newapi-key-rotator，支持每个实例监控多个渠道、同时对接 ezlinkai 和 new-api，并引入供货商账户实现渠道级访问控制。

**Architecture:** 每个 `(InstanceConfig, channelID)` 对映射为一个 `instance`（平铺 slice），`Server` 在 Basic Auth 阶段解析账户类型（管理员 / 供货商），通过 context 传递，各 handler 按权限过滤或拦截请求。

**Tech Stack:** Go 1.22，标准库 (`net/http`, `crypto/subtle`, `context`, `encoding/json`)，无第三方依赖。

---

## 文件变更总览

| 文件 | 操作 | 职责 |
|------|------|------|
| `config.go` | 修改 | 新增 `ChannelIDs []int`、`Platform`、可选 `UserID`、`AccountConfig`/`ChannelRef` |
| `config_test.go` | 新建 | 测试 ChannelIDs 解析、AccountConfig 解析、向后兼容 |
| `client.go` | 修改 | `New-Api-User` 头条件附加 |
| `rotator.go` | 修改 | 新增 `label string` 字段用于日志 |
| `auth.go` | 新建 | `Account` 类型、context helpers、`canAccess()` |
| `auth_test.go` | 新建 | 测试 `canAccess` 和账户解析逻辑 |
| `main.go` | 修改 | `instance` 加 `instIdx`/`channelID`；多渠道循环创建 |
| `server.go` | 修改 | `withAuth` 改为账户解析；所有 handler 加权限守卫；`/api/me` 端点 |
| `web/index.html` | 修改 | 加载时获取 `/api/me`，供货商隐藏管理员按钮 |
| `.env.example` | 修改 | 补充新变量示例 |

---

## Task 1: config.go — ChannelIDs、Platform、可选 UserID

**Files:**
- Modify: `config.go`

- [ ] **Step 1: 写测试（config_test.go）**

新建 `config_test.go`：

```go
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
```

- [ ] **Step 2: 运行测试，确认失败**

```
cd /path/to/newapi-key-rotator
go test ./... -run "TestLoadConfig" -v
```

期望：编译失败（`ChannelIDs` 字段不存在）或测试失败。

- [ ] **Step 3: 修改 config.go**

在 `config.go` 中，将 `InstanceConfig` 里的 `ChannelID int` 改为 `ChannelIDs []int`，新增 `Platform string`，并让 `UserID` 变为可选：

```go
type InstanceConfig struct {
	BaseURL     string
	AccessToken string
	UserID      string // 可选；new-api 需要，ezlinkai 不需要
	ChannelIDs  []int  // 至少一个渠道 ID
	Platform    string // 可选标签，如 "ezlinkai" / "newapi"
	Insecure    bool
}
```

将 `loadInstanceFromEnv` 函数替换为如下实现（注意去掉 `UserID` 的 required 检查，新增 `ChannelIDs` 解析）：

```go
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

	// CHANNEL_IDS 优先于 CHANNEL_ID（向后兼容）
	rawIDs := strings.TrimSpace(os.Getenv(channelIDsKey))
	rawSingle := strings.TrimSpace(os.Getenv(channelKey))
	if rawIDs == "" && rawSingle == "" {
		missing = append(missing, channelKey+" or "+channelIDsKey)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	if rawIDs != "" {
		for _, part := range strings.Split(rawIDs, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.Atoi(part)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("%s contains invalid channel ID %q", channelIDsKey, part)
			}
			inst.ChannelIDs = append(inst.ChannelIDs, id)
		}
	} else {
		id, err := strconv.Atoi(rawSingle)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%s must be a positive integer, got %q", channelKey, rawSingle)
		}
		inst.ChannelIDs = []int{id}
	}
	return inst, nil
}
```

在 `LoadConfig()` 中更新调用：

```go
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
```

同时删除 `Config` 中已有的 `WebUsername` / `WebPassword` 等字段（这些已存在，无需改动）。

- [ ] **Step 4: 运行测试，确认通过**

```
go test ./... -run "TestLoadConfig" -v
```

期望：4 个测试全部 PASS。

- [ ] **Step 5: 确认编译通过**

```
go build ./...
```

期望：编译失败（`ChannelID` 在 main.go / rotator.go / server.go 中仍被引用）。此时列出所有编译错误，下一步修复。

---

## Task 2: config.go — AccountConfig

**Files:**
- Modify: `config.go`
- Modify: `config_test.go`

- [ ] **Step 1: 在 config_test.go 追加 Account 测试**

```go
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
```

- [ ] **Step 2: 在 config.go 追加 AccountConfig 结构体和解析**

在 `config.go` 文件末尾（`parseDuration` 函数之前）追加：

```go
// ChannelRef 引用一个监控单元：哪个 InstanceConfig（按 0-based 索引）+ 哪个渠道 ID。
type ChannelRef struct {
	InstIdx   int
	ChannelID int
}

// AccountConfig 是一个供货商账户，拥有渠道级访问权限。
type AccountConfig struct {
	Password string
	Label    string
	Channels []ChannelRef
}
```

在 `Config` 结构体中追加 `Accounts` 字段：

```go
type Config struct {
	Instances    []*InstanceConfig
	Accounts     []*AccountConfig  // 供货商账户，空则仅管理员可登录
	DataDir      string
	PollInterval time.Duration
	HTTPTimeout  time.Duration
	WebListen    string
	WebUsername  string
	WebPassword  string
}
```

在 `LoadConfig()` 末尾（`return c, nil` 之前）追加账户解析：

```go
for n := 1; ; n++ {
    p := fmt.Sprintf("ACCOUNT_%d_", n)
    pw := strings.TrimSpace(os.Getenv(p + "PASSWORD"))
    if pw == "" {
        break
    }
    acc := &AccountConfig{
        Password: pw,
        Label:    strings.TrimSpace(os.Getenv(p + "LABEL")),
    }
    rawChs := strings.TrimSpace(os.Getenv(p + "CHANNELS"))
    for _, part := range strings.Split(rawChs, ",") {
        part = strings.TrimSpace(part)
        if part == "" {
            continue
        }
        sub := strings.SplitN(part, ":", 2)
        if len(sub) != 2 {
            return nil, fmt.Errorf("ACCOUNT_%d_CHANNELS: invalid entry %q (want instIdx:channelID)", n, part)
        }
        instIdx, err1 := strconv.Atoi(strings.TrimSpace(sub[0]))
        chID, err2 := strconv.Atoi(strings.TrimSpace(sub[1]))
        if err1 != nil || err2 != nil || instIdx < 0 || chID <= 0 {
            return nil, fmt.Errorf("ACCOUNT_%d_CHANNELS: invalid entry %q", n, part)
        }
        acc.Channels = append(acc.Channels, ChannelRef{InstIdx: instIdx, ChannelID: chID})
    }
    c.Accounts = append(c.Accounts, acc)
}
```

- [ ] **Step 3: 运行 Account 测试**

```
go test ./... -run "TestLoadConfig_Accounts" -v
```

期望：PASS。

- [ ] **Step 4: 提交**

```
git add config.go config_test.go
git commit -m "feat(config): ChannelIDs multi-channel, Platform, optional UserID, AccountConfig"
```

---

## Task 3: client.go — 条件附加 New-Api-User 头

**Files:**
- Modify: `client.go`

- [ ] **Step 1: 修改 `do()` 方法**

找到 `client.go` 中的：

```go
req.Header.Set("Authorization", c.token)
req.Header.Set("New-Api-User", c.userID)
```

替换为：

```go
req.Header.Set("Authorization", c.token)
if c.userID != "" {
    req.Header.Set("New-Api-User", c.userID)
}
```

- [ ] **Step 2: 确认编译**

```
go build ./...
```

期望：仍有来自 main.go / server.go 的编译错误（`ChannelID` 引用），这是正常的，留到后续任务修复。

- [ ] **Step 3: 提交**

```
git add client.go
git commit -m "fix(client): make New-Api-User header optional for ezlinkai compat"
```

---

## Task 4: rotator.go — label 字段

**Files:**
- Modify: `rotator.go`

- [ ] **Step 1: 修改 Rotator 结构体，新增 label**

在 `rotator.go` 的 `Rotator` 结构体里新增 `label string`：

```go
type Rotator struct {
	instCfg  *InstanceConfig
	cfg      *Config
	client   *Client
	store    *Store
	label    string // 日志标识，如 "ezlinkai/ch-42"

	mu               sync.Mutex
	paused           bool
	// ... 其余字段不变
}
```

修改 `NewRotator` 函数签名和实现：

```go
func NewRotator(label string, instCfg *InstanceConfig, cfg *Config, client *Client, store *Store) *Rotator {
	return &Rotator{
		label:   label,
		instCfg: instCfg,
		cfg:     cfg,
		client:  client,
		store:   store,
		paused:  store.GetPaused(),
	}
}
```

将 `tick()` 中所有 `log.Printf("INFO channel #%d ..."` 改为使用 `r.label`。例如：

```go
// 原来
log.Printf("INFO channel #%d re-enable with same key: %v", chID, err)
// 改为
log.Printf("INFO [%s] re-enable with same key: %v", r.label, err)
```

依次替换 `tick()` 中所有 `channel #%d` 格式，共约 8 处，全部改为 `[%s]` + `r.label`。

- [ ] **Step 2: 编译检查**

```
go build ./...
```

期望：`NewRotator` 调用方（`main.go`）报错，说明需要传 `label`，这在下一步修复。

- [ ] **Step 3: 提交**

```
git add rotator.go
git commit -m "feat(rotator): add label field for structured logging"
```

---

## Task 5: auth.go — 账户类型和权限辅助函数

**Files:**
- Create: `auth.go`
- Create: `auth_test.go`

- [ ] **Step 1: 写测试 auth_test.go**

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func makeServer(admin string, accounts []*AccountConfig) *Server {
	cfg := &Config{
		WebUsername: "admin",
		WebPassword: admin,
		Accounts:    accounts,
	}
	return &Server{cfg: cfg, instances: nil}
}

func TestCanAccess_admin(t *testing.T) {
	s := makeServer("secret", nil)
	inst := &instance{instIdx: 0, channelID: 42}
	acc := Account{IsAdmin: true}
	if !s.canAccess(acc, inst) {
		t.Error("admin should access any instance")
	}
}

func TestCanAccess_supplier_allowed(t *testing.T) {
	s := makeServer("secret", nil)
	inst := &instance{instIdx: 0, channelID: 42}
	acc := Account{IsAdmin: false, Channels: []ChannelRef{{InstIdx: 0, ChannelID: 42}}}
	if !s.canAccess(acc, inst) {
		t.Error("supplier should access authorized channel")
	}
}

func TestCanAccess_supplier_denied(t *testing.T) {
	s := makeServer("secret", nil)
	inst := &instance{instIdx: 0, channelID: 99}
	acc := Account{IsAdmin: false, Channels: []ChannelRef{{InstIdx: 0, ChannelID: 42}}}
	if s.canAccess(acc, inst) {
		t.Error("supplier should not access unauthorized channel")
	}
}

func TestResolveAccount_admin(t *testing.T) {
	s := makeServer("adminpw", nil)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("admin", "adminpw")
	acc, ok := s.resolveAccount(r)
	if !ok {
		t.Fatal("should resolve admin")
	}
	if !acc.IsAdmin {
		t.Error("should be admin")
	}
}

func TestResolveAccount_supplier(t *testing.T) {
	accounts := []*AccountConfig{
		{Password: "pw-a", Label: "供货商A", Channels: []ChannelRef{{InstIdx: 0, ChannelID: 42}}},
	}
	s := makeServer("adminpw", accounts)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("供货商A", "pw-a")
	acc, ok := s.resolveAccount(r)
	if !ok {
		t.Fatal("should resolve supplier")
	}
	if acc.IsAdmin {
		t.Error("should not be admin")
	}
	if acc.Label != "供货商A" {
		t.Errorf("want label=供货商A, got %q", acc.Label)
	}
}

func TestResolveAccount_invalid(t *testing.T) {
	s := makeServer("adminpw", nil)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("admin", "wrongpw")
	_, ok := s.resolveAccount(r)
	if ok {
		t.Error("should not resolve with wrong password")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```
go test ./... -run "TestCanAccess|TestResolveAccount" -v
```

期望：编译失败（`Account`、`canAccess`、`resolveAccount` 未定义）。

- [ ] **Step 3: 创建 auth.go**

```go
package main

import (
	"context"
	"crypto/subtle"
	"net/http"
)

// Account 表示已验证的请求方。
type Account struct {
	IsAdmin  bool
	Label    string
	Channels []ChannelRef // 供货商可访问的渠道列表；IsAdmin=true 时忽略
}

type accountContextKey struct{}

// getAccount 从请求 context 取出 Account。调用前须经过 withAuth 中间件。
func getAccount(r *http.Request) Account {
	acc, _ := r.Context().Value(accountContextKey{}).(Account)
	return acc
}

// resolveAccount 解析 Basic Auth 凭据并返回对应账户。
// 先检查管理员，再遍历供货商账户（仅比对密码，用户名不限）。
func (s *Server) resolveAccount(r *http.Request) (Account, bool) {
	_, pass, ok := r.BasicAuth()
	if !ok {
		return Account{}, false
	}

	// 管理员：用户名 + 密码都要匹配（如果设置了 WebPassword）
	if s.cfg.WebPassword != "" {
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.WebPassword)) == 1
		if passOK {
			return Account{IsAdmin: true, Label: s.cfg.WebUsername}, true
		}
	}

	// 供货商：仅比对密码
	for _, acc := range s.cfg.Accounts {
		if subtle.ConstantTimeCompare([]byte(pass), []byte(acc.Password)) == 1 {
			return Account{
				IsAdmin:  false,
				Label:    acc.Label,
				Channels: acc.Channels,
			}, true
		}
	}
	return Account{}, false
}

// canAccess 报告 acc 是否有权访问 inst。
func (s *Server) canAccess(acc Account, inst *instance) bool {
	if acc.IsAdmin {
		return true
	}
	for _, ref := range acc.Channels {
		if ref.InstIdx == inst.instIdx && ref.ChannelID == inst.channelID {
			return true
		}
	}
	return false
}

// withAuth 是全局 Basic Auth 中间件，将解析后的 Account 注入 context。
func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.cfg.WebPassword == "" {
		// 无密码保护：所有请求视为管理员（打印告警）
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), accountContextKey{}, Account{IsAdmin: true})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc, ok := s.resolveAccount(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="key-rotator"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), accountContextKey{}, acc)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin 返回 403 如果当前账户不是管理员。
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if getAccount(r).IsAdmin {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "需要管理员权限"})
	return false
}

// requireChannelAccess 返回 403 如果当前账户无权访问 inst，或 inst 不存在返回 404。
func (s *Server) requireChannelAccess(w http.ResponseWriter, r *http.Request, inst *instance) bool {
	if !s.canAccess(getAccount(r), inst) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "无权访问此渠道"})
		return false
	}
	return true
}
```

- [ ] **Step 4: 运行测试**

```
go test ./... -run "TestCanAccess|TestResolveAccount" -v
```

期望：5 个测试全部 PASS。

- [ ] **Step 5: 提交**

```
git add auth.go auth_test.go
git commit -m "feat(auth): Account type, resolveAccount, canAccess, withAuth middleware"
```

---

## Task 6: main.go — 多渠道 instance 创建

**Files:**
- Modify: `main.go`

- [ ] **Step 1: 修改 instance 结构体，新增 instIdx 和 channelID**

将 `instance` 结构体改为：

```go
type instance struct {
	instIdx   int // 对应 cfg.Instances 的下标
	channelID int // 该实例监控的具体渠道 ID
	cfg       *InstanceConfig
	store     *Store
	rotator   *Rotator
	trigger   chan struct{}
}
```

- [ ] **Step 2: 更新 main() 中的实例创建循环**

将原有的单层 `for i, instCfg := range cfg.Instances` 循环改为双层循环：

```go
instances := make([]*instance, 0)
for i, instCfg := range cfg.Instances {
    platformLabel := instCfg.Platform
    if platformLabel == "" {
        platformLabel = fmt.Sprintf("inst-%d", i)
    }
    for _, chID := range instCfg.ChannelIDs {
        label := fmt.Sprintf("%s/ch-%d", platformLabel, chID)
        poolFile := fmt.Sprintf("pool-%d-%d.json", i, chID)
        store, err := NewStore(filepath.Join(cfg.DataDir, poolFile))
        if err != nil {
            log.Fatalf("store error (%s): %v", label, err)
        }
        if store.IsDeleted() {
            log.Printf("INFO [%s] is deleted — skipping", label)
            continue
        }
        client := NewClient(instCfg, cfg)
        trigger := make(chan struct{}, 1)
        rotator := NewRotator(label, instCfg, cfg, client, store)
        instances = append(instances, &instance{
            instIdx:   i,
            channelID: chID,
            cfg:       instCfg,
            store:     store,
            rotator:   rotator,
            trigger:   trigger,
        })
    }
}
```

- [ ] **Step 3: 编译**

```
go build ./...
```

期望：main.go 编译通过；server.go 可能仍有 `inst.cfg.ChannelID` 引用报错，下一步修复。

- [ ] **Step 4: 提交**

```
git add main.go
git commit -m "feat(main): multi-channel instance creation with instIdx/channelID"
```

---

## Task 7: server.go — 权限守卫 + handleMe

**Files:**
- Modify: `server.go`

- [ ] **Step 1: 删除旧 withAuth 方法**

`server.go` 中原有的 `withAuth` 方法已移到 `auth.go`，直接删除 `server.go` 中的 `withAuth` 函数（共约 15 行）。同时删除顶部 `"crypto/subtle"` 的 import（已移入 auth.go）。

- [ ] **Step 2: 更新 handleInstances**

将原 `handleInstances` 完全替换为（新增 `instIdx`、`platform`、`is_admin` 字段，并按账户过滤）：

```go
func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	acc := getAccount(r)
	type instanceInfo struct {
		Index     int    `json:"index"`
		BaseURL   string `json:"base_url"`
		ChannelID int    `json:"channel_id"`
		InstIdx   int    `json:"inst_idx"`
		Platform  string `json:"platform"`
		Label     string `json:"label"`
		IsAdmin   bool   `json:"is_admin"`
	}
	infos := make([]instanceInfo, 0, len(s.instances))
	for i, inst := range s.instances {
		if inst.store.IsDeleted() {
			continue
		}
		if !s.canAccess(acc, inst) {
			continue
		}
		infos = append(infos, instanceInfo{
			Index:     i,
			BaseURL:   inst.cfg.BaseURL,
			ChannelID: inst.channelID,
			InstIdx:   inst.instIdx,
			Platform:  inst.cfg.Platform,
			Label:     inst.store.GetLabel(),
			IsAdmin:   acc.IsAdmin,
		})
	}
	writeJSON(w, http.StatusOK, infos)
}
```

- [ ] **Step 3: 更新 handleInstanceChannelID**

原代码中 `inst.cfg.ChannelID` 改为 `inst.channelID`（两处）：

```go
// 原
effective := inst.store.ChannelID(inst.cfg.ChannelID)
// ...
"default_channel_id": inst.cfg.ChannelID,
// 改为
effective := inst.store.ChannelID(inst.channelID)
// ...
"default_channel_id": inst.channelID,
```

以及 `log.Printf` 中的 `inst.cfg.ChannelID` 改为 `inst.channelID`（约 4 处）。

- [ ] **Step 4: 添加权限守卫到各 handler**

在每个 `handleInstance*` 方法里，`getInstance` 之后插入权限检查：

**供货商可访问（查看状态 + 提交 Key）的 handler**，在 `inst, ok := s.getInstance(r)` 之后加：

```go
if !ok {
    http.NotFound(w, r)
    return
}
if !s.requireChannelAccess(w, r, inst) {
    return
}
```

涉及：`handleInstanceStatus`、`handleInstanceKeys`、`handleInstanceKeysAppend`。

**管理员专用 handler**，在 `getInstance` 之后加：

```go
if !ok {
    http.NotFound(w, r)
    return
}
if !s.requireAdmin(w, r) {
    return
}
```

涉及：`handleInstanceRotateNow`、`handleInstancePause`、`handleInstanceResume`、`handleInstanceDelete`、`handleInstanceLabel`（POST 分支）、`handleInstanceChannelID`（POST 分支）。

`handleInstanceLabel` GET 分支和 `handleInstanceChannelID` GET 分支改为 `requireChannelAccess`。

- [ ] **Step 5: 添加 /api/me 端点**

在 `Handler()` 的 `mux` 注册里追加：

```go
mux.HandleFunc("/api/me", s.handleMe)
```

新增方法：

```go
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	acc := getAccount(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"is_admin": acc.IsAdmin,
		"label":    acc.Label,
	})
}
```

- [ ] **Step 6: 更新 handleIndex 以使用请求中的实际凭据**

将原来硬编码 `WebUsername:WebPassword` 的 token 注入改为从请求中读取：

```go
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 使用请求中的实际凭据，供货商/管理员各自注入自己的 token
	user, pass, _ := r.BasicAuth()
	token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	page := bytes.Replace(indexHTML, []byte("__AUTH_TOKEN__"), []byte(token), 1)
	_, _ = w.Write(page)
}
```

- [ ] **Step 7: 编译**

```
go build ./...
```

期望：编译通过（零错误）。

- [ ] **Step 8: 运行所有测试**

```
go test ./... -v
```

期望：全部 PASS。

- [ ] **Step 9: 提交**

```
git add server.go
git commit -m "feat(server): supplier access control, handleMe, per-handler permission guards"
```

---

## Task 8: web/index.html — 供货商感知 UI

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: 在 JS 中加载 /api/me 并根据角色调整 UI**

在 `<script>` 开头新增全局变量 `let isAdmin = true;`。

将页面加载的立即执行函数改为：

```javascript
let isAdmin = true;

(async () => {
  // 获取当前账户角色
  try {
    const r = await apiFetch('api/me', { headers: { 'Accept': 'application/json' } });
    const me = await r.json();
    isAdmin = me.is_admin;
  } catch(e) {}

  // 根据角色隐藏管理员专用按钮
  if (!isAdmin) {
    document.getElementById('btn-rotate').style.display = 'none';
    document.getElementById('btn-pause').style.display = 'none';
    document.getElementById('btn-delete').style.display = 'none';
    document.getElementById('label-row').style.display = 'none';
    // 隐藏渠道 ID 编辑入口
    document.getElementById('ch-edit-link').style.display = 'none';
  }

  await reloadInstances();
  setInterval(() => refresh(), 5000);
})();
```

给 `.label-row` 加上 `id="label-row"`（在 HTML 中找到该 div，添加 id 属性）：

```html
<div class="label-row" id="label-row">
```

- [ ] **Step 2: 更新 renderTabs 展示 platform 信息**

将 `renderTabs` 中的 tab 文本改为包含 platform：

```javascript
function renderTabs() {
  const bar = document.getElementById('tab-bar');
  bar.innerHTML = '';
  instances.forEach((inst) => {
    const btn = document.createElement('button');
    const isActive = inst.index === activeIdx;
    btn.className = 'tab-btn' + (isActive ? ' active' : '');
    const platformTag = inst.platform ? `[${inst.platform}] ` : '';
    btn.textContent = inst.label
      ? `${platformTag}${inst.label} · #${inst.channel_id}`
      : `${platformTag}实例${inst.index} · #${inst.channel_id}`;
    btn.onclick = () => {
      activeIdx = inst.index;
      document.getElementById('label-input').value = inst.label || '';
      renderTabs();
      refresh();
    };
    bar.appendChild(btn);
  });
  const cur = instances.find(inst => inst.index === activeIdx);
  document.getElementById('label-input').value = cur?.label || '';
}
```

- [ ] **Step 3: 编译（确认 embed 正常）**

```
go build ./...
```

- [ ] **Step 4: 提交**

```
git add web/index.html
git commit -m "feat(ui): supplier-aware web console, hide admin buttons, show platform in tabs"
```

---

## Task 9: .env.example 和 Docker Compose 更新

**Files:**
- Modify: `.env.example`

- [ ] **Step 1: 更新 .env.example**

将 `.env.example` 完全替换为：

```bash
# ============================================================
# 实例 1（ezlinkai）— 无需 USER_ID
# ============================================================
NEWAPI_BASE_URL=https://ezlinkai.example.com
NEWAPI_ACCESS_TOKEN=sk-admin-xxx
# NEWAPI_USER_ID=        # ezlinkai 不需要
NEWAPI_PLATFORM=ezlinkai
CHANNEL_IDS=42,88         # 多渠道逗号分隔，或用 CHANNEL_ID=42（单渠道）

# ============================================================
# 实例 2（new-api）— 需要 USER_ID
# ============================================================
INSTANCE_2_BASE_URL=https://newapi.example.com
INSTANCE_2_ACCESS_TOKEN=sk-admin-yyy
INSTANCE_2_USER_ID=1
INSTANCE_2_PLATFORM=newapi
INSTANCE_2_CHANNEL_IDS=15,23

# ============================================================
# 管理员账户（完整权限）
# ============================================================
WEB_LISTEN=:8080
WEB_USERNAME=admin
WEB_PASSWORD=change-me-admin

# ============================================================
# 供货商账户（渠道级权限：仅查看 + 提交 Key）
# 格式：ACCOUNT_N_CHANNELS=instIdx:channelID,...
# instIdx 从 0 开始，对应上面实例的顺序（实例1=0, 实例2=1）
# ============================================================
ACCOUNT_1_PASSWORD=supplier-a-pw
ACCOUNT_1_CHANNELS=0:42,0:88
ACCOUNT_1_LABEL=供货商A

ACCOUNT_2_PASSWORD=supplier-b-pw
ACCOUNT_2_CHANNELS=1:15
ACCOUNT_2_LABEL=供货商B

# ============================================================
# 其他配置
# ============================================================
POLL_INTERVAL=60s
HTTP_TIMEOUT=15s
DATA_DIR=/data
# INSECURE_SKIP_VERIFY=false
```

- [ ] **Step 2: 运行完整测试套件**

```
go test ./... -v
```

期望：全部 PASS。

- [ ] **Step 3: 最终编译**

```
go build ./...
```

- [ ] **Step 4: 提交**

```
git add .env.example
git commit -m "docs: update .env.example with multi-instance, multi-channel, supplier accounts"
```

---

## 验收清单

手动验证：

1. **管理员登录**：用 `WEB_USERNAME`/`WEB_PASSWORD` 打开控制台，所有实例/渠道可见，"立即检查"/"暂停"/"删除"按钮正常显示。
2. **供货商登录**：用 `ACCOUNT_1_LABEL`/`ACCOUNT_1_PASSWORD` 登录，只看到授权渠道，管理员按钮不显示，"提交 Key" 正常可用。
3. **供货商越权**：直接 `curl -u "供货商A:pw-a" POST /api/instance/2/rotate-now` → 403。
4. **ezlinkai 兼容**：未填 `USER_ID` 的实例正常轮询（日志无 `New-Api-User` 错误）。
5. **多渠道**：CHANNEL_IDS=42,88 启动后控制台出现两个 tab，各自独立 key 池。
6. **向后兼容**：旧配置（`CHANNEL_ID=42`、无 `ACCOUNT_*`）不改动仍正常工作。

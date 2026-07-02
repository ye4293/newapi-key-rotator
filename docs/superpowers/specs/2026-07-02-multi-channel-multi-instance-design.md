# 设计文档：多实例多渠道 Key 自动轮换服务

**日期**：2026-07-02  
**仓库**：`newapi-key-rotator`（扩展现有代码）

---

## 背景与目标

现有 `newapi-key-rotator` 每个实例只能监控一个渠道，且强制要求 `New-Api-User` 鉴权头（new-api 专用）。目标：

1. 支持 **ezlinkai** 和 **new-api** 两套部署同时监控
2. 每个实例可配置**多个渠道**，每个渠道独立维护 key 池
3. 单一 Docker Compose 部署，统一 Web 控制台

---

## API 兼容性差异

| 差异点 | new-api | ezlinkai |
|--------|---------|----------|
| 鉴权头 | `Authorization` + `New-Api-User: <uid>` | `Authorization` 仅此一个 |
| 路由 `GET /api/channel/:id` | ✅ | ✅ |
| 路由 `PUT /api/channel/` | ✅ | ✅ |
| 响应格式 `{success, message, data}` | ✅ | ✅ |
| 渠道状态码 1/2/3 | ✅ | ✅ |

**处理方案**：`UserID` 改为可选字段。`New-Api-User` 头仅在 `UserID != ""` 时附加，以此兼容两套系统。

---

## 配置方案（环境变量）

向后兼容：原有单渠道写法 `CHANNEL_ID=42` 继续有效。

```bash
# 实例 1（ezlinkai）- 无需 USER_ID
NEWAPI_BASE_URL=https://ezlinkai.example.com
NEWAPI_ACCESS_TOKEN=sk-admin-xxx
NEWAPI_PLATFORM=ezlinkai         # 可选，用于日志/控制台标签
CHANNEL_IDS=42,88                # 多渠道逗号分隔（与 CHANNEL_ID 二选一）

# 实例 2（new-api）- 需要 USER_ID
INSTANCE_2_BASE_URL=https://newapi.example.com
INSTANCE_2_ACCESS_TOKEN=sk-admin-yyy
INSTANCE_2_USER_ID=1             # new-api 必填
INSTANCE_2_PLATFORM=newapi
INSTANCE_2_CHANNEL_IDS=15,23

# 管理员账户（完整权限，看所有渠道）
WEB_USERNAME=admin
WEB_PASSWORD=admin-secret

# 供货商账户（渠道级权限）
ACCOUNT_1_PASSWORD=supplier-a-pw
ACCOUNT_1_CHANNELS=0:42,0:88    # 格式：instIdx:channelID，逗号分隔
ACCOUNT_1_LABEL=供货商A

ACCOUNT_2_PASSWORD=supplier-b-pw
ACCOUNT_2_CHANNELS=1:15
ACCOUNT_2_LABEL=供货商B
```

解析逻辑：
- `CHANNEL_IDS` 优先于 `CHANNEL_ID`（向后兼容）
- `PLATFORM` 默认为空字符串，日志中显示 `inst-{n}`
- `ACCOUNT_N_*` 从 N=1 开始连续扫描，空缺则停止

---

## 访问控制设计

### 账户模型

系统有两类账户：

**管理员账户**（通过 `WEB_USERNAME` / `WEB_PASSWORD` 配置）：
- 查看所有渠道状态
- 提交/追加 Key
- 触发立即检查
- 暂停/恢复监控

**供货商账户**（通过 `ACCOUNT_N_*` 配置）：
- 仅查看被授权的渠道状态（状态、池剩余量）
- 仅向被授权的渠道提交/追加 Key
- **不能**：触发轮换、暂停监控、看其他渠道

### 权限矩阵

| 操作 | 管理员 | 供货商（自己渠道） | 供货商（其他渠道） |
|------|--------|------------------|------------------|
| 查看状态 | ✅ | ✅ | ❌ 403 |
| 提交 Key | ✅ | ✅ | ❌ 403 |
| 追加 Key | ✅ | ✅ | ❌ 403 |
| 触发轮换 | ✅ | ❌ 403 | ❌ 403 |
| 暂停/恢复 | ✅ | ❌ 403 | ❌ 403 |

### 服务端实现要点

- Basic Auth 验证阶段解析密码 → 匹配账户 → 挂到 `gin.Context`
- 中间件：`resolveAccount()`——从请求头提取账户信息
- 权限检查：`requireAdmin()` 和 `requireChannelAccess(inst, ch)` 两个守卫函数
- `GET /api/status`：管理员返回全部，供货商只返回被授权渠道的子集
- 越权请求返回 `403 Forbidden`，不泄露其他渠道是否存在

### Web 控制台 UI 区分

供货商登录后：
- 只显示被授权的渠道卡片
- 无"立即检查"/"暂停"按钮
- 无其他实例/渠道的任何信息

管理员登录后：
- 完整控制台，按实例分组显示所有渠道

---

## 架构设计

### 核心数据结构

```
Config
├── Instances []*InstanceConfig
│   ├── BaseURL, AccessToken, UserID (可选), Platform
│   └── ChannelIDs []int
├── Accounts []*AccountConfig       ← 新增
│   ├── Password, Label
│   └── Channels []ChannelRef      ← {InstIdx, ChannelID}
├── AdminUsername, AdminPassword
├── DataDir, PollInterval, HTTPTimeout
└── WebListen
```

**监控单元** = `(instIdx int, channelID int)` 二元组，对应：
- 一个 `Rotator`（运行轮换逻辑）
- 一个 `Store`（管理 key 池，pool 文件：`pool-{instIdx}-{channelID}.json`）
- 共用同一个 `*Client`（实例级，复用 HTTP 连接池）

### 组件变更

| 文件 | 变更说明 |
|------|---------|
| `config.go` | `ChannelID int` → `ChannelIDs []int`；新增 `Platform string`；`UserID` 改可选；新增 `AccountConfig` 解析 |
| `client.go` | `do()` 中 `New-Api-User` 头改为条件附加（`userID != ""`） |
| `store.go` | `NewStore(path)` 不变；调用方传入 `pool-{n}-{id}.json` 路径 |
| `rotator.go` | 新增 `label string` 字段用于日志标识（`"ezlinkai/ch-42"`）；其余逻辑不变 |
| `main.go` | 遍历所有 `(inst, channelID)` 对，为每对创建 `Client`/`Rotator`/`Store`，并发 goroutine |
| `server.go` | 路由加 `?inst=&ch=` 参数；`/api/status` 返回所有单元列表；新增 `resolveAccount()` 中间件和权限守卫 |
| `auth.go` | 新文件：`resolveAccount()`、`requireAdmin()`、`requireChannelAccess()` |

---

## 轮换逻辑

继承现有 `tick()` 逻辑，无变化：

```
tick(instIdx, channelID):
  1. GetChannel(channelID)            → 失败: 记录 lastError，跳过
  2. status != auto-disabled?         → 清除 pendingRotation，退出
  3. !pendingRotation                 → ReEnableChannel（同 key 重启测试）
                                         设 pendingRotation=true，等下轮
  4. pendingRotation 且仍 auto-disabled
     ├── pool.PeekNext() 无 key      → 日志告警，渠道保持禁用
     └── 有 key                      → ApplyKeyAndEnable(newKey)
                                         → 成功: pool.CommitAdvance()
```

**不变量**：
- 手动禁用（status=2）永远不触发轮换
- pool 文件原子写入（tmp → rename），重启安全
- 多监控单元并发，互不干扰

---

## HTTP API（Web 控制台）

所有接口受 Basic Auth 保护。

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/status` | 返回所有监控单元状态数组 |
| `GET` | `/api/status?inst=0&ch=42` | 单个单元状态 |
| `POST` | `/api/keys?inst=0&ch=42` | 整批替换 key 池 |
| `POST` | `/api/keys/append?inst=0&ch=42` | 追加 key 到池 |
| `POST` | `/api/rotate-now?inst=0&ch=42` | 立即触发检查 |
| `POST` | `/api/pause?inst=0&ch=42` | 暂停/恢复监控 |

`/api/status` 响应示例：

```json
[
  {
    "inst": 0,
    "platform": "ezlinkai",
    "channel_id": 42,
    "label": "ezlinkai/ch-42",
    "paused": false,
    "last_status": 1,
    "pending_rotation": false,
    "last_action": "rotated to key 2/5 (****1234) and re-enabled",
    "last_error": "",
    "last_checked": "2026-07-02T10:00:00Z",
    "pool": {"total": 5, "index": 2, "remaining": 3, "exhausted": false, "current_key": "****1234"}
  }
]
```

---

## Web 控制台 UI

管理员登录后，按实例分组展示所有渠道：

```
[ezlinkai]
  渠道 #42  状态: 启用  当前Key: ****1234  池: 3/5  [提交Key] [立即检查] [暂停]
  渠道 #88  状态: 自动禁用  池: 0/0 (已耗尽)  [提交Key] [立即检查] [暂停]

[newapi]
  渠道 #15  状态: 启用  当前Key: ****abcd  池: 8/10  [提交Key] [立即检查] [暂停]
```

供货商（如供货商A，授权渠道 0:42, 0:88）登录后：

```
[供货商A]
  渠道 #42  状态: 启用  当前Key: ****1234  池: 3/5  [提交Key]
  渠道 #88  状态: 自动禁用  池: 0/0 (已耗尽)  [提交Key]
```

---

## Docker Compose

```yaml
services:
  rotator:
    build: .
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    env_file: .env
```

`.env.example` 更新包含两实例示例配置。

---

## 不在本次范围内

- Webhook / 钉钉 / 邮件通知（可后续扩展）
- 多-key 渠道（`MultiKeyInfo`）支持
- 自动余额检测触发轮换

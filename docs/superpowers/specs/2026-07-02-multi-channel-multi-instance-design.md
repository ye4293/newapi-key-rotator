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
```

解析逻辑：
- `CHANNEL_IDS` 优先于 `CHANNEL_ID`（向后兼容）
- `PLATFORM` 默认为空字符串，日志中显示 `inst-{n}`

---

## 架构设计

### 核心数据结构

```
Config
├── Instances []*InstanceConfig
│   ├── BaseURL, AccessToken, UserID (可选), Platform
│   └── ChannelIDs []int
├── DataDir, PollInterval, HTTPTimeout
└── WebListen, WebUsername, WebPassword
```

**监控单元** = `(instIdx int, channelID int)` 二元组，对应：
- 一个 `Rotator`（运行轮换逻辑）
- 一个 `Store`（管理 key 池，pool 文件：`pool-{instIdx}-{channelID}.json`）
- 共用同一个 `*Client`（实例级，复用 HTTP 连接池）

### 组件变更

| 文件 | 变更说明 |
|------|---------|
| `config.go` | `ChannelID int` → `ChannelIDs []int`；新增 `Platform string`；`UserID` 改可选 |
| `client.go` | `do()` 中 `New-Api-User` 头改为条件附加（`userID != ""`） |
| `store.go` | `NewStore(path)` 不变；调用方传入 `pool-{n}-{id}.json` 路径 |
| `rotator.go` | 新增 `label string` 字段用于日志标识（`"ezlinkai/ch-42"`）；其余逻辑不变 |
| `main.go` | 遍历所有 `(inst, channelID)` 对，为每对创建 `Client`/`Rotator`/`Store`，并发 goroutine |
| `server.go` | 路由加 `?inst=&ch=` 参数；`/api/status` 返回所有单元列表 |

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

现有单页面改为按实例分组展示：

```
[ezlinkai]
  渠道 #42  状态: 启用  当前Key: ****1234  池: 3/5  [提交Key] [立即检查]
  渠道 #88  状态: 自动禁用  池: 0/0 (已耗尽)  [提交Key]

[newapi]
  渠道 #15  状态: 启用  当前Key: ****abcd  池: 8/10  [提交Key] [立即检查]
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

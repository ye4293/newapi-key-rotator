# newapi-key-rotator

一个**独立**的小服务（不修改 new-api 源码），通过 new-api 的管理 API 监控**单个单-key 渠道**：当该渠道被 new-api 自动禁用（key 失效 / 余额不足等）时，自动从一批备用 key 里取下一个换上并重新启用渠道。配套一个**网页控制台**，可在运行时随时提交 / 替换 key 池，无需改文件、无需重启容器。

## 工作原理

- 每隔 `POLL_INTERVAL`（默认 60s）调用 `GET /api/channel/:id` 读取渠道状态。
- 仅当状态为 **3（自动禁用）** 时才动作：取 key 池里下一个 key，通过 `PUT /api/channel/`（读取-改-回写，替换 `key` 并把 `status` 设为启用）换上并启用渠道。
- **手动禁用（状态 2）不会被碰**——视为你的主动操作。
- key 池**用尽后停止**：渠道保持自动禁用，日志告警，直到你提交新的一批 key。
- 进度持久化在 `DATA_DIR/pool.json`，重启不丢；通过控制台提交新一批 key 时，进度重置到第一个。
- key 明文**绝不**写日志、也不在控制台回显（只显示数量与末 4 位）。

## 前置条件

1. 在 new-api 把目标渠道建成**单 key 渠道**（不要用多-key 模式，这样并发集中到单个 key）。
2. 在 new-api **个人设置 → 生成系统访问令牌**，得到访问令牌；记下该用户的**用户 ID**，且该用户须为**管理员**。
3. 记下目标渠道的**渠道 ID**。

## 快速开始（Docker）

```bash
cp .env.example .env
# 编辑 .env，填好 NEWAPI_BASE_URL / NEWAPI_ACCESS_TOKEN / NEWAPI_USER_ID / CHANNEL_ID / WEB_PASSWORD
docker compose up -d --build
docker compose logs -f
```

打开 `http://<服务器IP>:8080`（浏览器会用 `WEB_USERNAME` / `WEB_PASSWORD` 弹窗登录），把一批 key（每行一个）粘进文本框，点「保存并立即应用」。

> ⚠️ 控制台管理的是 API key，请务必设置 `WEB_PASSWORD`，并放在反向代理 / 防火墙 / 内网之后，不要直接裸露公网。

## 配置（环境变量）

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `NEWAPI_BASE_URL` | 是 | — | new-api 地址，去掉结尾斜杠，如 `https://api.example.com` |
| `NEWAPI_ACCESS_TOKEN` | 是 | — | 管理员系统访问令牌 |
| `NEWAPI_USER_ID` | 是 | — | 令牌所属管理员用户 ID（`New-Api-User` 头） |
| `CHANNEL_ID` | 是 | — | 目标单 key 渠道 ID |
| `WEB_LISTEN` | 否 | `:8080` | 控制台监听地址 |
| `WEB_USERNAME` | 否 | `admin` | 控制台 Basic Auth 用户名 |
| `WEB_PASSWORD` | 否 | （空） | 控制台 Basic Auth 密码；**留空则不鉴权并打印告警** |
| `POLL_INTERVAL` | 否 | `60s` | 轮询间隔（Go duration，如 `30s`、`2m`） |
| `HTTP_TIMEOUT` | 否 | `15s` | 对 new-api 的单次请求超时 |
| `DATA_DIR` | 否 | `/data` | `pool.json` 存放目录 |
| `INSECURE_SKIP_VERIFY` | 否 | `false` | new-api 用自签证书时设 `true` |

## Web / HTTP API

控制台页面之外，也可直接调用（受同一 Basic Auth 保护）：

- `GET  /api/status` —— 返回渠道与 key 池状态 JSON。
- `POST /api/keys` —— body `{"keys":"k1\nk2\nk3"}`，整批替换 key 池并重置进度，随后立即触发一次检查。
- `POST /api/rotate-now` —— 立即触发一次检查（不必等下个周期）。

```bash
curl -u admin:change-me -X POST http://localhost:8080/api/keys \
  -H 'Content-Type: application/json' \
  -d '{"keys":"sk-aaa\nsk-bbb\nsk-ccc"}'
```

## 本地运行（需要本机装 Go 1.22+）

```bash
export NEWAPI_BASE_URL=... NEWAPI_ACCESS_TOKEN=... NEWAPI_USER_ID=1 CHANNEL_ID=123 WEB_PASSWORD=change-me DATA_DIR=./data
go run .
```

## 验证清单

1. 在 new-api 后台把目标渠道手动改成「自动禁用」（或等它真的失效），控制台提交一批 key，观察日志在一个周期内换上第一个 key 并把渠道恢复「启用」。
2. 池里只放 1 个 key，重复让渠道失效，确认第二次出现「池已用尽」告警且不再改动。
3. 重新提交一批不同的 key，确认进度归零、`current_key` 末 4 位随之变化。
4. 故意写错令牌，确认日志报清晰的 401 鉴权错误且服务持续重试、不崩溃。

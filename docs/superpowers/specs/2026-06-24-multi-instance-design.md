# Multi-Instance Support Design

**Date:** 2026-06-24  
**Status:** Approved

## Goal

Allow a single `newapi-key-rotator` process to monitor and auto-rotate keys for multiple independent new-api instances simultaneously. Each instance has its own BaseURL, access token, user ID, channel ID, and key pool.

## Configuration

`InstanceConfig` holds per-instance fields: `BaseURL`, `AccessToken`, `UserID`, `ChannelID`, `Insecure`.

`Config` holds shared fields: `DataDir`, `PollInterval`, `HTTPTimeout`, `WebListen`, `WebUsername`, `WebPassword`.

`LoadConfig` reads instance 0 from the existing canonical env vars (`NEWAPI_BASE_URL`, `NEWAPI_ACCESS_TOKEN`, `NEWAPI_USER_ID`, `CHANNEL_ID`, `INSECURE_SKIP_VERIFY`) for full backward compatibility. Additional instances are discovered by scanning for `INSTANCE_2_BASE_URL`, `INSTANCE_3_BASE_URL`, etc. — scanning stops at the first missing prefix.

## Data Layer

`NewStore` changes signature from `NewStore(dataDir string)` to `NewStore(poolPath string)`. The caller constructs the path:
- Instance 0: `<DataDir>/pool.json` (backward compatible with existing data)
- Instance N: `<DataDir>/pool_N.json`

`client.go` and `rotator.go` change from accepting `*Config` to `(*InstanceConfig, *Config)`, using `instCfg` for per-instance fields and `cfg` for shared fields.

## Runtime

`main.go` defines a local `instance` struct `{cfg *InstanceConfig, store *Store, rotator *Rotator, trigger chan struct{}}`. It iterates over `cfg.Instances`, constructs one tuple per instance, launches one background goroutine per rotator, and passes the full slice to `NewServer`.

## Server & API

`Server` holds `[]*instance`. New routes (Go 1.22 path params):

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/instances` | Returns `[{index, base_url, channel_id}]` for UI init |
| GET | `/api/instance/{idx}/status` | Per-instance status |
| POST | `/api/instance/{idx}/keys` | Replace key pool |
| POST | `/api/instance/{idx}/keys/append` | Append to key pool |
| POST | `/api/instance/{idx}/rotate-now` | Manual trigger |

Legacy routes (`/api/status`, `/api/keys`, `/api/keys/append`, `/api/rotate-now`) are preserved and internally delegate to instance 0.

## Web UI

On load: `GET /api/instances` → render tab bar (`实例 0 · #10246`, `实例 1 · #19`, …). Active tab tracked in `activeIdx`. All status fetches and key submits go to `/api/instance/{activeIdx}/...`. Auto-refresh every 5 s applies only to the active tab.

## .env Addition

```
INSTANCE_2_BASE_URL=http://45.78.201.134:3000
INSTANCE_2_ACCESS_TOKEN=gy27Lmcp/d50w6L/wcd8qV3lwZl5sYE8
INSTANCE_2_USER_ID=1
INSTANCE_2_CHANNEL_ID=19
```

## Files Changed

| File | Change |
|------|--------|
| `config.go` | Add `InstanceConfig`, refactor `LoadConfig` to scan numbered env vars |
| `store.go` | `NewStore(dataDir)` → `NewStore(poolPath)` |
| `client.go` | `NewClient(cfg)` → `NewClient(instCfg, cfg)` |
| `rotator.go` | Accept `*InstanceConfig`; replace `cfg.ChannelID` with `instCfg.ChannelID` |
| `main.go` | Loop over instances, construct per-instance tuples, pass slice to server |
| `server.go` | Hold `[]*instance`, add instance-scoped routes, keep legacy routes |
| `web/index.html` | Fetch `/api/instances` on load, render tabs, scope all API calls to active tab |
| `.env` | Append four `INSTANCE_2_*` lines |

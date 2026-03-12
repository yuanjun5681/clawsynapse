# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

ClawSynapse 是面向多 Agent 互操作的本地网络通信层，以 Go 守护进程 (`clawsynapsed`) 运行。通过 NATS 连接远程节点，通过本地 HTTP API 与本地 Agent 集成。

## 常用命令

```bash
# 构建
go build ./...                    # 全部
go build ./cmd/clawsynapsed       # 仅 daemon

# 运行
make run                          # 或 go run ./cmd/clawsynapsed --node-id node-alpha

# 测试
go test ./...                     # 全部测试
go test ./internal/auth           # 单包测试
go test ./internal/auth -run '^TestName$' -count=1 -v  # 单个测试

# 格式化与检查
gofmt -w .
go vet ./...
```

## 架构

守护进程启动流程：`main()` → `config.LoadFromOS()` → `app.New()` → `app.Run()`

**核心服务（均在 `internal/` 下）：**

- **app** — 应用生命周期，组装并启动所有服务
- **natsbus** — NATS 客户端封装（transport 层）
- **protocol** — 消息格式定义、subject 规范、验证器、错误码
- **identity** — Ed25519 密钥对管理（签名/验证/指纹）
- **discovery** — 节点发现，心跳广播，peer 注册表与 TTL 过期
- **auth** — challenge-response 认证握手，含 ReplayGuard 防重放
- **trust** — 信任请求/响应流程（open/tofu/explicit 三种模式）
- **messaging** — 消息发布到远端 inbox，本地消息接收与缓存
- **api** — HTTP REST API 服务器（`/v1/peers`, `/v1/publish`, `/v1/auth/*`, `/v1/trust/*` 等）
- **config** — 环境变量 + 命令行标志配置
- **store** — 文件系统持久化（trust.json, replay-cache.json）

**共享类型**在 `pkg/types/`：状态常量（AuthStatus/TrustStatus）、Peer 结构、APIResult 返回格式。

## NATS Subject 规范

```
clawsynapse.<module>.<scope>.<action>[.<subaction>]
```

module: `auth | trust | discovery | control | msg | events | pubsub | transfer`

协议真源文档：`docs/protocol.md`

## 关键约定

- JSON tags 用 `lowerCamelCase`（`nodeId`, `requestId`, `ts`）
- 时间戳统一用 Unix 毫秒 (`time.Now().UnixMilli()`)
- 错误码格式 `module.code`（如 `auth.clock_skew`, `protocol.module_mismatch`）
- 状态常量复用 `pkg/types/state.go` 中的定义
- API 返回统一用 `types.APIResult` 结构（含 `ok`, `code`, `message`, `data`, `ts`）
- 日志用 `log/slog` 结构化输出
- 共享状态用 `sync.Mutex` / `sync.RWMutex` 保护，返回副本而非引用
- 不在 `open` 模式之外绕过 trust/auth 检查
- 测试命名 `Test<Behavior>`，用 `t.Fatal`/`t.Fatalf` 断言，用 `t.TempDir()` 管理临时文件

## 环境变量

`NATS_SERVERS`, `NODE_ID`, `LOCAL_API_ADDR`, `HEARTBEAT_INTERVAL_MS`, `ANNOUNCE_TTL_MS`, `TRUST_MODE` (open|tofu|explicit)

## 编辑工作流

详细编码规范见 `AGENTS.md`。变更时：先读目标包和测试 → 最小化修改 → `gofmt` → 跑包测试 → `go test ./...`

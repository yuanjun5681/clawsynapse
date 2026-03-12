---
summary: "ClawSynapse 运行与配置：Go 实现、目录结构、配置项与阶段规划"
title: "ClawSynapse Operations"
---

# ClawSynapse Operations

最后更新：2026-03-12

## Go 实现

Go 用于实现 ClawSynapse：

- 并发模型覆盖长期订阅、心跳、握手和超时控制
- 打包为单二进制守护进程
- 标准库覆盖网络、HTTP 与加密支持
- 部署为本地设备上的长期运行进程

## 依赖

- NATS：`github.com/nats-io/nats.go`
- WebSocket：`github.com/coder/websocket`
- 日志：`log/slog`
- 加密：标准库 `crypto/ed25519`、`crypto/sha256`、`crypto/rand`
- HTTP：标准库 `net/http`

## 目录结构

```text
cmd/clawsynapsed/main.go
internal/config/
internal/protocol/
internal/natsbus/
internal/discovery/
internal/auth/
internal/bridge/
internal/adapter/
internal/adapter/openclaw/
internal/api/
internal/store/
pkg/types/
```

## 并发模型

- 一个 goroutine 管理 NATS 生命周期
- 一个 goroutine 管理节点心跳
- subscription handler 按消息独立处理
- 一个后台清理协程处理过期 peer、握手超时和去重缓存
- 每个外部调用使用 `context.WithTimeout`

## 配置

```bash
NATS_SERVERS=nats://127.0.0.1:4222
NODE_ID=node-alpha
NODE_CAPABILITIES=chat,tools
AGENT_ADAPTER=openclaw
GATEWAY_URL=ws://127.0.0.1:18789
GATEWAY_TOKEN=xxx
HEARTBEAT_INTERVAL_MS=15000
ANNOUNCE_TTL_MS=30000
TRUST_MODE=tofu
IDENTITY_KEY_PATH=~/.clawsynapse/identity.key
LOCAL_API_ADDR=127.0.0.1:18080
```

## 实现阶段

### v1

- NATS 连接与订阅
- 节点发现与 peer 表
- 本节点 inbox 收发
- `OpenClawAdapter`
- 本地 `publish` / `peers` API
- `open` / `tofu` 基础信任模式

### v2

- challenge-response 完整握手
- 消息签名与重放保护
- `request-reply`
- 去重与失败重试

### v3

- 事件转发
- 死信队列
- 观测与诊断接口
- 管理命令集合

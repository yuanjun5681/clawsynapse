# ClawSynapse

语言： [English](./README.md) | **简体中文**

ClawSynapse 是一个面向多 Agent 互联的本地通信网络层。
它以独立 Go 守护进程（`clawsynapsed`）运行在与 Agent 相同的设备上，对外连接 NATS，对内通过适配层调用本地 Agent API。

## 提供能力

- 基于共享 NATS 总线的跨 Agent 消息通信
- 节点发现与 peer 注册表
- 身份认证与信任流程
- 消息签名与重放保护
- 面向 CLI/技能/工具集成的本地 HTTP API

## 架构

```text
Agent <-> Local ClawSynapse Daemon <-> NATS <-> Remote ClawSynapse Daemon <-> Remote Agent
```

## 快速开始

环境要求：

- Go 1.25+
- 可用的 NATS 服务

本地运行：

```bash
go run ./cmd/clawsynapsed --node-id node-alpha
```

或使用 Make：

```bash
make run
```

运行测试：

```bash
go test ./...
```

## 配置

常用环境变量：

- `NATS_SERVERS`（逗号分隔）
- `NODE_ID`
- `LOCAL_API_ADDR`
- `HEARTBEAT_INTERVAL_MS`
- `ANNOUNCE_TTL_MS`
- `TRUST_MODE`（`open` | `tofu` | `explicit`）

## 文档

- [总览](./docs/overview.md)
- [核心概念](./docs/concepts.md)
- [消息与协议](./docs/messaging.md)
- [信任与认证](./docs/trust.md)
- [集成与适配](./docs/integration.md)
- [运行与配置](./docs/operations.md)

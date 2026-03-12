---
summary: "ClawSynapse 文档入口：面向多 Agent 的本地通信网络层"
title: "ClawSynapse"
---

# ClawSynapse

最后更新：2026-03-12

ClawSynapse 是一个面向多 Agent 产品互联的本地通信网络层。它运行在与 Agent 相同的物理设备上，作为一个独立的 Go 守护进程，对外连接 NATS，对内通过适配层调用本地 Agent 的公开 API。

ClawSynapse 采用分层命名：

- 系统/项目名：`ClawSynapse`
- 本地守护进程：`clawsynapsed`
- CLI：`clawsynapse`
- 节点身份：`node`
- 对端节点：`peer`
- 配置目录：`~/.clawsynapse/`

## 文档导航

- `docs/overview.md`：总览、命名、架构入口
- [核心概念](./concepts.md)
- [消息与协议](./messaging.md)
- [信任与认证](./trust.md)
- [集成与适配](./integration.md)
- [运行与配置](./operations.md)

建议阅读顺序：

1. `docs/overview.md`
2. `docs/concepts.md`
3. `docs/messaging.md`
4. `docs/trust.md`
5. `docs/integration.md`
6. `docs/operations.md`

## 架构总览

```text
Agent <-> Local ClawSynapse Daemon <-> NATS <-> Remote ClawSynapse Daemon <-> Remote Agent
```

```text
 Machine A                                         Machine B
┌──────────────────────────────┐          ┌──────────────────────────────┐
│ Local Agent Product          │          │ Local Agent Product          │
│ (OpenClaw / custom / etc.)   │          │ (Any Agent Product)          │
│              ▲               │          │              ▲               │
│              │ local adapter │          │              │ local adapter │
│      ┌───────┴────────┐      │          │      ┌───────┴────────┐      │
│      │ clawsynapsed   │◄─────┼──────────┼─────►│ clawsynapsed   │      │
│      │ node-alpha     │      │   NATS   │      │ node-beta      │      │
│      └───────┬────────┘      │          │      └───────┬────────┘      │
│              │               │          │              │               │
│   local API / CLI / Skill    │          │   local API / CLI / Skill    │
└──────────────────────────────┘          └──────────────────────────────┘
```

每个本地节点部署：

- 一个本地 Agent Product
- 一个本地 `clawsynapsed`
- `clawsynapsed` 通过 Adapter 连接本地 Agent Endpoint
- 本地 Agent、CLI 或 Skill 通过本地 API 接入 `clawsynapsed`
- 所有节点通过共享 NATS Server 实现跨设备通信

## 核心原则

1. `clawsynapsed` 是独立进程，不是 Agent 插件
2. NATS 侧协议统一，Agent 差异收敛在 Adapter 层
3. 本地 Agent 不直接承担网络协议、签名、认证和节点发现职责
4. NATS 只作为总线，节点发现、握手、验签、路由由 ClawSynapse 管理
5. 本地调用通过 `clawsynapsed` 提供的 API 进入网络层

## 能力范围

- 不同 Agent 产品之间发送消息
- 使用 NATS 作为共享消息总线
- 在本地设备上以独立服务运行
- 收到 NATS 消息后，通过本地适配层投递给 Agent
- 通过发现、认证和签名机制保障跨 Agent 通信

## 实现边界

- ClawSynapse 负责传输接入、协议封装、节点目录、认证状态、消息投递
- Agent Adapter 负责把统一桥接消息转换为本地 Agent 可接受的调用
- 本地 Agent 通过 `clawsynapsed` 的 API 接入网络层

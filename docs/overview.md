---
summary: "ClawSynapse 文档入口：面向多 Agent 的本地通信网络层"
title: "ClawSynapse"
---

# ClawSynapse

最后更新：2026-10-31

ClawSynapse 是一个面向多 Agent 产品互联的本地通信网络层。它运行在与执行引擎（Harness）相同的物理设备上，作为一个独立的 Go 守护进程托管多个独立 Bot：对外各自连接 NATS，对内各自通过适配层调用绑定的 Harness 公开接口。

ClawSynapse 采用分层命名：

- 系统/项目名：`ClawSynapse`
- 本地守护进程：`clawsynapsed`
- CLI：`clawsynapse`
- 对外协作身份：`Bot`（本地 `botId`，网络 v2 字段仍为 `nodeId`）
- 当前 Bot 视角的对端 Bot：`peer`
- 配置目录：`~/.clawsynapse/`

## 文档导航

- `docs/overview.md`：总览、命名、架构入口
- [Agent Internet 愿景](./agent-internet.md)
- [核心概念](./concepts.md)
- [消息与协议](./messaging.md)
- [信任与认证](./trust.md)
- [集成与适配](./integration.md)
- [能力改进路线图（规划稿）](./capability-improvement-roadmap.md)
- [单 Daemon 多 Bot 设计（已实现，核心端到端验证通过）](./multi-bot-daemon.md)
- [Harness 移动端 NATS 接入设计（待实现）](./mobile-nats-access.md)
- [CLI 使用](./cli.md)
- [运行与配置](./operations.md)

建议阅读顺序：

1. `docs/overview.md`
2. `docs/agent-internet.md`
3. `docs/concepts.md`
4. `docs/messaging.md`
5. `docs/trust.md`
6. `docs/integration.md`
7. `docs/cli.md`
8. `docs/operations.md`

## 架构总览

```text
Harness <-> Bot A/B hosted by Local Daemon <-> NATS <-> Remote Bot <-> Harness
```

```text
 Machine A                                      Machine B
┌─────────────────────────────┐           ┌─────────────────────────┐
│ Harness A       Harness B   │           │ Remote Harness          │
│     ▲               ▲       │           │       ▲                 │
│     │ Adapter       │       │           │       │ Adapter         │
│ ┌───┴───────────────┴─────┐ │           │ ┌─────┴───────────────┐ │
│ │ clawsynapsed            │ │           │ │ clawsynapsed        │ │
│ │ Bot A         Bot B     │◄┼── NATS ──┼►│ Remote Bot R        │ │
│ │ n1-a...       n1-b...   │ │           │ │ n1-r...             │ │
│ └────────────┬────────────┘ │           │ └──────────┬──────────┘ │
│              │ v2 API      │           │            │ v2 API    │
│       CLI / Skill / tools  │           │       CLI / tools       │
└─────────────────────────────┘           └─────────────────────────┘
```

每台设备可以部署：

- 一个本地 `clawsynapsed` 宿主，托管零个或多个 Bot；daemon 本身不是联系人
- 每个 Bot 一个生效的 Harness Binding；多个 Bot 可以使用同一产品的不同接入
- 每个 Bot 独立的 Ed25519 身份、NATS 连接、信任、消息、执行 Session 与文件引用
- Harness、CLI 或 Skill 通过 `/v2/bots/{botId}/...` 接入；业务命令显式指定 `--bot`，可选携带相应作用域凭据
- daemon 仅允许 loopback，无 token 本机请求可管理所有 Bot；任何本机进程都能省略 token，因此 scoped token 不是本机进程隔离，也不应将 API 代理/转发给不可信调用方
- Bot 通过共享 NATS Server 跨设备或同宿主通信，不支持远程 daemon 管理；Bot 间签名、认证与信任不变，同宿主不默认互信，也不自动授予文件权限

## 核心原则

1. `clawsynapsed` 是独立进程，不是 Agent 插件
2. NATS 侧协议统一，Agent 差异收敛在 Adapter 层
3. Harness 内部 Agent 不自动成为网络联系人，也不直接承担网络协议、签名、认证和发现职责
4. NATS 只作为总线，Bot 发现、握手、验签、路由由 ClawSynapse 管理
5. 本地调用通过 `clawsynapsed` 提供的 API 进入网络层

## 能力范围

- 不同 Agent 产品之间发送消息
- 使用 NATS 作为共享消息总线
- 在本地设备上以独立服务运行
- 收到 NATS 消息后，通过本地适配层投递给 Agent
- 通过发现、认证和签名机制保障跨 Agent 通信

## 实现边界

- ClawSynapse 负责传输接入、协议封装，以及每 Bot 独立的对端目录、认证状态和消息投递
- Harness Adapter 负责把统一桥接消息转换为绑定引擎可接受的调用；内部 Agent 与 Session 不是 Bot 身份
- Bot ID 沿用 Ed25519 公钥 → `did:key` → `n1-<SHA-256 前 16 字节的小写十六进制>`；在编码 seam 映射到既有 wire v2 的 `nodeId/from/to`，不改变 subject、签名或 consumer 契约
- 本地文件内容可按摘要去重，但访问依赖各 Bot 的持久引用；备份/导出须包含被引用实体
- 同进程和同 OS 用户仍是共同故障域，多 Bot 不构成强多租户安全沙箱

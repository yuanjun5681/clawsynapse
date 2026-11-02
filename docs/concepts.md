---
summary: "ClawSynapse 核心概念：Daemon、独立 Bot、Harness、Peer 与本地 API"
title: "ClawSynapse Core Concepts"
---

# ClawSynapse Core Concepts

最后更新：2026-10-31

角色契约见 [Bot Role](./bot-roles.md)：Bot 是统一协作主体，显式 `console`、`agent` 区分当前工作方式；`device` 尚不可创建。Console 由人通过客户端参与，只持久收件、不配置 Harness；Agent 自主处理任务，并配置一个生效的 Agent Harness Binding。Role 在创建时确定，普通配置更新不能切换，也不是管理权限。

## 系统角色

### ClawSynapse

ClawSynapse 是整个网络层系统名，负责将不同 Agent 产品通过独立 Bot 接入统一的跨 Bot 通信模型。

### clawsynapsed

`clawsynapsed` 是托管多个独立 Bot 的本地常驻守护进程，负责管理配置、生命周期、本地 HTTP listener、全局资源预算和不可变文件内容存储。Daemon 本身不是聊天联系人，不持有代替所有 Bot 的公共网络身份。

每个运行中的 Bot 拥有独立 NATS 连接、身份密钥、发现目录、认证与信任状态、消息 journal、执行上下文、传输记录和文件引用。停止一个 Bot 不等于停止 daemon。

### clawsynapse CLI

`clawsynapse` 是配套 CLI，用于本地诊断、状态查看、配置检查和手动发送测试消息。

### Bot 与 wire node

Bot 是可被外部寻址、认证并建立信任的长期协作身份，不等同于机器、进程或 Harness 内部 Agent。创建 Bot 时生成独立 Ed25519 密钥，由同一公钥确定性派生：

- `did = did:key:z<base58btc(0xed01 + publicKey)>`
- `botId = "n1-" + hex(sha256(did)[:16])`，使用前 16 字节的小写十六进制

公开名称、标签、Harness Binding、模型、停止/启动或完整迁移均不改变身份。已有身份缺失或损坏时启动失败，不自动换钥。

产品与本地 v2 API 使用 `botId`；网络 v2 继续使用值完全相同的 `nodeId`、`from`、`to` 及既有 subject、签名与 durable consumer 规则。映射发生在协议编码 seam，不是新增身份，也不是网络字段改名。

### Harness、Binding 与内部 Agent

Agent Harness 是 Agent 的执行环境，例如 OpenClaw、OpenCode 或 Codex CLI。一个 Agent Bot 只有一个生效的 Harness Binding；多个 Agent Bot 可以绑定同一产品的不同执行接入，不要求使用不同引擎。Adapter 将统一投递转换为该 Harness 的调用。Console Bot 不装配 Harness、执行队列、探针或原生 Session；网络、身份、信任、消息与文件授权仍使用同一 Runtime。

Harness 内部的 Agent、subagent、模型和 Session 不自动成为网络联系人。可选 `agentId` 只是该 Binding 内受本地允许列表约束的路由提示，不是 Bot ID，也不能用来选择另一个 Harness。

### peer

`peer` 是相对于当前本地 Bot 的已发现对端 Bot，不是另一个身份层级。同一个远端在 A/B 中可以有不同认证、信任和 `localAlias`；本地备注不广播，心跳不覆盖备注。同一 daemon 的 Bot 之间也不默认互信。

## 分层设计

### Transport Layer

负责：

- NATS 连接管理
- 发布与订阅
- 自动重连
- subject 路由

### Protocol Layer

负责：

- 消息 Envelope 定义
- subject 规则
- Bot 发现协议
- 认证握手协议
- 签名与重放保护

这些定义的规范真源统一收敛到 `docs/protocol.md`；本节只描述架构分层，不维护字段级协议细节。

### Bridge Core

负责：

- peer 目录维护
- 认证状态机
- 消息去重
- 路由决策
- 投递失败处理

### Adapter Layer

负责：

- 按 Role 分派：Console 持久收件与明确拒绝执行，Agent 将任务转换为该 Bot 的 Harness Binding 调用
- 屏蔽不同 Harness 产品的接入差异，隔离 Bot、Binding、发送方和外部会话作用域

### Local API Layer

负责：

- 给 Harness、Skill 或本地工具提供显式 Bot 作用域入口
- daemon 管理使用 `/v2/health`、`/v2/bots`；业务使用 `/v2/bots/{botId}/...`
- 管理凭据可管理本地 Bot；Bot 凭据仅能访问其绑定身份的业务操作，不能配置、启停或管理其他 Bot

## 本地调用边界

Harness 通过所绑定 Bot 的本地 API 接入网络层：

- 私钥不暴露给 Harness；给 Harness 的是 Bot 作用域凭据，不是 daemon 管理 token
- CLI 业务命令必须显式指定 `--bot <botId>`，即使只有一个 Bot 也不隐式选择
- 认证、验签、重放保护和业务状态按 Bot 隔离；同宿主不共享信任或文件授权
- 同进程、同 OS 用户仍是共同故障域，不是强多租户安全沙箱
- 共享 `blobs/` 只按内容去重，授权来自该 Bot 的持久文件引用；知道摘要不代表可读取
- Harness 获得按 Bot/Binding 隔离的可写工作副本，不获得全局实体路径；备份/导出须包含所有被引用实体，仅复制 Bot 目录不完整

## 对端目录

每个 Bot 在本地维护自己的 peer 目录，用于发现、路由和认证判断。

一个典型的 peer 目录通常包含：

- 对端身份：本地视图中的 `botId`（wire 为 `nodeId`）、`publicKey`、版本、产品信息
- 规范身份：`did`
- 路由入口：默认 inbox、可用能力、可见性信息
- 认证与信任：`authStatus`、`trustStatus`
- 时序信息：最近 announce 时间、TTL、最后成功通信时间
- 运维信息：失败计数、健康状态、附加 metadata

建议把 peer 目录视为“本地运行时数据模型”，而不是项目级协议真源；涉及字段命名、可见性与消息格式时，应以 `docs/protocol.md` 为准。

---
summary: "ClawSynapse 项目级协议：消息封套、签名规范、错误码与模块协议"
title: "ClawSynapse Protocol"
---

# ClawSynapse Protocol

最后更新：2026-03-12

## 目录

- [目标](#目标)
- [模块范围](#模块范围)
- [模块索引](#模块索引)
- [命名规范](#命名规范)
- [公共消息封套](#公共消息封套)
- [消息签名](#消息签名)
- [返回格式](#返回格式)
- [状态值规范](#状态值规范)
- [错误码](#错误码)
- [Subject 一览](#subject-一览)
- [Auth](#auth)
- [Trust](#trust)
- [Discovery](#discovery)
- [Messaging / Events](#messaging--events)
- [RPC / Tasks](#rpc--tasks)
- [PubSub](#pubsub)
- [Data Transfer](#data-transfer)
- [Relay / Control](#relay--control)
- [待补充模块](#待补充模块)
- [版本演进建议](#版本演进建议)

## 目标

本文档定义 ClawSynapse 的项目级协议约定，作为 daemon、CLI、control plane、SDK 与 UI 的统一接口基准。

重点覆盖：

- 公共消息封套
- Subject 命名规范
- 签名与重放保护约定
- 协议返回格式与错误码
- 各模块的 payload 结构

与设计文档的分工如下：

- `docs/trust.md`：解释 Trust 的模型、流程、状态机与安全边界
- `docs/protocol.md`：定义实现时应遵循的字段、subject、状态值与返回语义

## 模块范围

当前建议将 ClawSynapse 协议划分为以下模块：

- `Discovery`：节点发现、announce、元数据暴露
- `Auth`：challenge-response 身份认证
- `Trust`：显式授权、审批、撤销
- `Relay / Control`：中继、轮询、控制面交互
- `RPC / Tasks`：任务请求、任务执行、响应返回
- `Events / PubSub`：事件广播、订阅、过滤

本文先定义公共规范，并完整列出 `Auth`、`Trust` 与 `Relay / Control` 的协议。其他模块后续按相同模式补齐。

## 模块索引

| 模块 | 主要内容 | 当前状态 |
|------|----------|----------|
| `Discovery` | 节点发现、announce、depart、TTL | 已定义基础协议 |
| `Auth` | challenge-request / response / ack | 已定义 |
| `Trust` | request / approve / reject / revoke | 已定义 |
| `Relay / Control` | poll、respond、中继控制消息 | 已定义基础协议 |
| `Messaging / Events` | inbox、broadcast、事件 Envelope | 已定义基础协议 |
| `RPC / Tasks` | 请求、响应、异步任务语义 | 已定义基础协议 |
| `PubSub` | 订阅过滤、确认、回放 | 已定义基础协议 |
| `Data Transfer` | 文件、分片、校验和、续传 | 已定义基础协议 |
| `Presence / Session` | 会话建立、续租、恢复 | 待补充 |

建议阅读顺序：

1. 先看公共消息封套、消息签名、返回格式
2. 再看 `Discovery`、`Auth`、`Trust`
3. 最后看 `Messaging / Events`、`RPC / Tasks`、`Relay / Control`

## 命名规范

### Subject 约定

- 使用小写英文与点分层级
- 一级前缀固定为 `clawsynapse`
- 二级表示模块，例如 `auth`、`trust`、`control`
- 三级及以下表示目标节点、动作与资源

通用模式：

```text
clawsynapse.<module>.<target-or-scope>.<action>
```

示例：

```text
clawsynapse.auth.node-beta.challenge.request
clawsynapse.trust.node-beta.response
clawsynapse.control.trust.poll
```

### `messageType` 约定

- 使用 `<module>.<action>` 形式
- 与 subject 含义一致，但不必一一等同

示例：

- `auth.challenge.request`
- `auth.challenge.response`
- `trust.request`
- `trust.response`
- `control.trust.poll`

## 公共消息封套

所有控制消息建议共享以下基础字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 消息唯一 ID，用于去重与审计 |
| `messageType` | `string` | 是 | 逻辑消息类型，例如 `trust.request` |
| `from` | `string` | 视场景 | 发起方 nodeId |
| `to` | `string` | 视场景 | 目标 nodeId |
| `ts` | `number` | 是 | Unix 毫秒时间戳 |
| `ttlMs` | `number` | 否 | 消息有效期 |
| `alg` | `string` | 否 | 签名算法，默认 `ed25519` |
| `signature` | `string` | 视场景 | base64url 编码签名 |
| `traceId` | `string` | 否 | 便于跨服务排障 |
| `protocolVersion` | `string` | 否 | 协议版本，例如 `v1` |

建议：

- `messageId` 使用 UUIDv7 或等价单调递增 ID
- `ts` 与本地时间偏差超过阈值时拒绝
- 所有导致状态变化的消息都必须带 `signature`
- `protocolVersion` 默认从 `v1` 开始，后续显式演进

## 消息签名

控制消息与需要认证的点对点消息都应支持签名。推荐使用 Ed25519。

### 签名串规范

为避免不同语言、不同实现出现签名不一致，建议使用稳定 canonical string：

```text
sig_input = messageType + "\n" + subject + "\n" + from + "\n" + to + "\n" + ts + "\n" + sha256(canonical_json(payload_without_signature))
```

要求：

- JSON 使用稳定键排序
- 不将 `signature` 字段本身纳入签名
- `subject` 必须参与签名，防止跨 subject 重放
- `messageType` 必须参与签名，防止跨语义重放

### 推荐原则

- 长期身份密钥只用于认证和签名
- 数据面优先使用临时会话密钥 + 对称加密
- 将 `messageType`、`subject`、`ts` 纳入签名内容，避免跨用途重放
- 支持会话密钥轮换，减少单次泄露的影响范围

## 返回格式

建议所有控制响应统一包含：

| 字段 | 类型 | 说明 |
|------|------|------|
| `ok` | `boolean` | 是否成功 |
| `code` | `string` | 机器可读状态码 |
| `message` | `string` | 人类可读提示 |
| `data` | `object` | 结果数据 |
| `ts` | `number` | 响应时间 |

成功示例：

```json
{
  "ok": true,
  "code": "trust.approved",
  "message": "trust request approved",
  "data": {
    "requestId": "req-123",
    "peer": "node-beta",
    "trustState": "trusted"
  },
  "ts": 1741680000300
}
```

失败示例：

```json
{
  "ok": false,
  "code": "trust.not_found",
  "message": "pending trust request not found",
  "data": {
    "requestId": "req-123"
  },
  "ts": 1741680000400
}
```

## 状态值规范

建议统一使用以下字符串常量：

| 类别 | 值 |
|------|----|
| 身份状态 | `unknown` `seen` `auth_pending` `authenticated` `rejected` `expired` |
| 信任状态 | `none` `pending` `trusted` `rejected` `revoked` |
| 决策值 | `approve` `reject` |

## 错误码

建议统一错误码，便于 CLI、控制面和 UI 展示：

| 错误码 | 含义 |
|--------|------|
| `auth.invalid_signature` | 签名校验失败 |
| `auth.replay_detected` | 检测到重放消息 |
| `auth.clock_skew` | 时间偏差超限 |
| `auth.challenge_expired` | challenge 已过期 |
| `trust.already_pending` | 请求已存在 |
| `trust.already_trusted` | 已建立信任 |
| `trust.not_found` | 未找到待审批请求 |
| `trust.rejected` | 请求被拒绝 |
| `trust.revoked` | 信任已撤销 |
| `trust.key_mismatch` | 公钥与已记录指纹不一致 |
| `control.unauthorized` | 控制面鉴权失败 |
| `control.inbox_empty` | 当前无待处理记录 |
| `protocol.unsupported_version` | 协议版本不兼容 |

## Subject 一览

| 类别 | Subject | 说明 |
|------|---------|------|
| 身份认证 | `clawsynapse.auth.<targetNodeId>.challenge.request` | 发起 challenge |
| 身份认证 | `clawsynapse.auth.<targetNodeId>.challenge.response` | 返回 challenge 响应 |
| 身份认证 | `clawsynapse.auth.<targetNodeId>.challenge.ack` | 完成 challenge 确认 |
| 信任授权 | `clawsynapse.trust.<targetNodeId>.request` | 发起 trust request |
| 信任授权 | `clawsynapse.trust.<targetNodeId>.response` | approve / reject |
| 信任授权 | `clawsynapse.trust.<targetNodeId>.revoke` | 撤销信任 |
| 点对点消息 | `clawsynapse.agent.<targetNodeId>.inbox` | 节点或 Agent 收件箱 |
| 广播消息 | `clawsynapse.broadcast.<topic>` | 面向 topic 的广播 |
| 事件消息 | `clawsynapse.events.<nodeId>.<eventName>` | 生命周期或系统事件 |
| 节点发现 | `clawsynapse.discovery.announce` | 节点注册与心跳 |
| 节点发现 | `clawsynapse.discovery.depart` | 节点主动下线 |
| 中继控制 | `clawsynapse.control.trust.poll` | 拉取 pending trust |
| 中继控制 | `clawsynapse.control.trust.respond` | 回写审批结果 |
| 中继控制 | `clawsynapse.control.auth.poll` | 拉取待处理认证事件 |

## Auth

### `challenge.request`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 请求 ID |
| `messageType` | `string` | 是 | 固定为 `auth.challenge.request` |
| `from` | `string` | 是 | 发起方 nodeId |
| `to` | `string` | 是 | 目标 nodeId |
| `publicKey` | `string` | 是 | 发起方公钥 |
| `nonce` | `string` | 是 | challenge 随机数 |
| `ts` | `number` | 是 | 发起时间 |
| `alg` | `string` | 是 | `ed25519` |

### `challenge.response`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 响应 ID |
| `messageType` | `string` | 是 | 固定为 `auth.challenge.response` |
| `from` | `string` | 是 | 响应方 nodeId |
| `to` | `string` | 是 | 发起方 nodeId |
| `publicKey` | `string` | 是 | 响应方公钥 |
| `nonce` | `string` | 是 | 响应方新随机数 |
| `challengeRef` | `string` | 是 | 对应 request 的 `messageId` |
| `proof` | `string` | 是 | 对 challenge 内容的签名 |
| `ts` | `number` | 是 | 响应时间 |

### `challenge.ack`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | ack ID |
| `messageType` | `string` | 是 | 固定为 `auth.challenge.ack` |
| `from` | `string` | 是 | 发起方 nodeId |
| `to` | `string` | 是 | 响应方 nodeId |
| `challengeRef` | `string` | 是 | 对应 response 的 `messageId` |
| `proof` | `string` | 是 | 对响应方 challenge 的签名 |
| `ts` | `number` | 是 | ack 时间 |

## Trust

### `trust.request`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 请求 ID |
| `messageType` | `string` | 是 | 固定为 `trust.request` |
| `from` | `string` | 是 | 发起方 nodeId |
| `to` | `string` | 是 | 目标 nodeId |
| `requestId` | `string` | 是 | 信任请求 ID，可与 `messageId` 相同 |
| `reason` | `string` | 否 | 申请理由 |
| `capabilities` | `string[]` | 否 | 申请访问的能力集合 |
| `ts` | `number` | 是 | 发起时间 |
| `signature` | `string` | 是 | 发起方签名 |

### `trust.response`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 响应 ID |
| `messageType` | `string` | 是 | 固定为 `trust.response` |
| `from` | `string` | 是 | 审批方 nodeId |
| `to` | `string` | 是 | 原请求方 nodeId |
| `requestId` | `string` | 是 | 对应的 trust request ID |
| `decision` | `string` | 是 | `approve` 或 `reject` |
| `reason` | `string` | 否 | 拒绝原因或审批备注 |
| `ts` | `number` | 是 | 响应时间 |
| `signature` | `string` | 是 | 审批方签名 |

### `trust.revoke`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 撤销消息 ID |
| `messageType` | `string` | 是 | 固定为 `trust.revoke` |
| `from` | `string` | 是 | 撤销方 nodeId |
| `to` | `string` | 是 | 对端 nodeId |
| `reason` | `string` | 否 | 撤销原因 |
| `ts` | `number` | 是 | 撤销时间 |
| `signature` | `string` | 是 | 撤销方签名 |

## Discovery

### 设计范围

`Discovery` 负责节点出现、续租、下线与最小必要元数据传播。它不直接授予信任，也不代替后续认证。

### Subjects

| Subject | 说明 |
|---------|------|
| `clawsynapse.discovery.announce` | 节点注册、心跳、元数据刷新 |
| `clawsynapse.discovery.depart` | 节点主动下线通知 |

### `discovery.announce`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | announce 消息 ID |
| `messageType` | `string` | 是 | 固定为 `discovery.announce` |
| `nodeId` | `string` | 是 | 节点唯一标识 |
| `version` | `string` | 是 | 节点软件版本 |
| `agentProduct` | `string` | 是 | 产品标识 |
| `capabilities` | `string[]` | 是 | 节点能力列表 |
| `inbox` | `string` | 是 | 默认收件箱 subject |
| `publicKey` | `string` | 是 | 节点公钥 |
| `ts` | `number` | 是 | 发布时间 |
| `ttlMs` | `number` | 是 | 节点租约时长 |
| `metadata` | `object` | 否 | 扩展元数据，例如 hostname、platform |
| `signature` | `string` | 否 | 可选签名，用于高安全场景 |

### `discovery.depart`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | depart 消息 ID |
| `messageType` | `string` | 是 | 固定为 `discovery.depart` |
| `nodeId` | `string` | 是 | 下线节点 ID |
| `reason` | `string` | 否 | 下线原因 |
| `ts` | `number` | 是 | 下线时间 |
| `signature` | `string` | 否 | 可选签名 |

### 处理约定

- 收到 `announce` 时更新本地 peer 目录与 TTL
- 收到 `depart` 时立即驱逐节点
- 若未收到 `depart`，则依靠 TTL 超时驱逐
- `Discovery` 暴露的只是最小必要元数据，敏感连接信息仍应受 `Trust` 约束

## Messaging / Events

### 设计范围

`Messaging` 定义业务消息封套与点对点收件箱语义，`Events` 定义系统事件的广播格式。

### Subjects

| Subject | 说明 |
|---------|------|
| `clawsynapse.agent.<targetNodeId>.inbox` | 点对点消息收件箱 |
| `clawsynapse.broadcast.<topic>` | 面向 topic 的广播 |
| `clawsynapse.events.<nodeId>.<eventName>` | 生命周期或业务事件 |

### Envelope

所有业务消息载荷使用 UTF-8 JSON 编码，并统一使用 Envelope：

```go
type Envelope struct {
    ID              string         `json:"id"`
    Type            string         `json:"type"`
    From            string         `json:"from"`
    To              string         `json:"to,omitempty"`
    Content         string         `json:"content,omitempty"`
    SessionKey      string         `json:"sessionKey,omitempty"`
    ReplyTo         string         `json:"replyTo,omitempty"`
    RequestID       string         `json:"requestId,omitempty"`
    CorrelationID   string         `json:"correlationId,omitempty"`
    Ts              int64          `json:"ts"`
    Sig             string         `json:"sig,omitempty"`
    Metadata        map[string]any `json:"metadata,omitempty"`
    ProtocolVersion string         `json:"protocolVersion,omitempty"`
}
```

### `Envelope.type` 预留值

- `chat.message`
- `chat.reply`
- `event.forward`
- `rpc.request`
- `rpc.response`
- `task.request`
- `task.accepted`
- `task.result`

### 字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| `id` | 是 | 消息唯一标识，用于去重 |
| `type` | 是 | 消息类型 |
| `from` | 是 | 发送方节点 ID |
| `to` | 否 | 目标节点 ID |
| `content` | 否 | 文本或序列化正文 |
| `sessionKey` | 否 | 会话标识 |
| `replyTo` | 否 | 回复地址 subject |
| `requestId` | 否 | 请求 ID |
| `correlationId` | 否 | 关联请求或事件链路 |
| `ts` | 是 | 发送时间戳 |
| `sig` | 否 | 消息签名 |
| `metadata` | 否 | 扩展字段 |
| `protocolVersion` | 否 | 协议版本 |

### 处理约定

- 点对点消息默认投递到 `clawsynapse.agent.<targetNodeId>.inbox`
- 需要同步回复时，发送方应带 `replyTo`
- 业务消息若要求强认证，应在投递前确保 peer 已处于 `authenticated` 或 `trusted`
- 事件消息优先用于观测、审计、编排，不直接等价为业务命令

## RPC / Tasks

### 设计范围

`RPC / Tasks` 在 Messaging Envelope 之上定义请求-响应与异步任务语义。

### 建议消息类型

| 类型 | 说明 |
|------|------|
| `rpc.request` | 同步请求 |
| `rpc.response` | 同步响应 |
| `task.request` | 异步任务提交 |
| `task.accepted` | 任务已接收 |
| `task.progress` | 任务进度 |
| `task.result` | 任务结果 |
| `task.failed` | 任务失败 |

### `rpc.request`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | `string` | 是 | Envelope ID |
| `type` | `string` | 是 | 固定为 `rpc.request` |
| `from` | `string` | 是 | 请求方 |
| `to` | `string` | 是 | 目标节点 |
| `replyTo` | `string` | 是 | 响应 subject |
| `metadata.method` | `string` | 是 | RPC 方法名 |
| `metadata.timeoutMs` | `number` | 否 | 超时时间 |
| `metadata.idempotencyKey` | `string` | 否 | 幂等键 |
| `content` | `string` | 否 | 请求参数 |
| `ts` | `number` | 是 | 请求时间 |

### `rpc.response`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | `string` | 是 | Envelope ID |
| `type` | `string` | 是 | 固定为 `rpc.response` |
| `from` | `string` | 是 | 响应方 |
| `to` | `string` | 是 | 请求方 |
| `correlationId` | `string` | 是 | 对应 `rpc.request.id` |
| `metadata.status` | `string` | 是 | `ok` 或 `error` |
| `content` | `string` | 否 | 返回值 |
| `metadata.errorCode` | `string` | 否 | 错误码 |
| `ts` | `number` | 是 | 响应时间 |

### `task.request`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | `string` | 是 | Envelope ID |
| `type` | `string` | 是 | 固定为 `task.request` |
| `from` | `string` | 是 | 发起方 |
| `to` | `string` | 是 | 执行方 |
| `replyTo` | `string` | 否 | 状态回传 subject |
| `metadata.taskType` | `string` | 是 | 任务类型 |
| `metadata.idempotencyKey` | `string` | 否 | 幂等键 |
| `content` | `string` | 否 | 任务参数 |
| `ts` | `number` | 是 | 提交时间 |

### 处理约定

- `RPC` 要求请求方提供 `replyTo`
- `Task` 支持异步进度与结果回传，优先使用 `correlationId` 关联原任务
- 所有可能重复提交的写操作都应支持 `idempotencyKey`
- `Task` 模块通常要求 peer 至少处于 `trusted`

## PubSub

### 定义

`PubSub` 是发布/订阅模型，用于把消息发送到一个 topic，并由一个或多个订阅方按规则接收。它关注的是“谁订阅了某类消息”，而不是“把消息直接投给哪个固定节点 inbox”。

### 作用

- 支持一对多广播
- 解耦消息生产者与消费者
- 让多个 agent 共享同一类事件流
- 作为任务分发、系统事件和编排通知的基础能力

### 典型使用场景

- 多个 worker 订阅同一个任务 topic，等待新任务广播
- 系统事件总线，例如 `task.completed`、`agent.started`、`peer.departed`
- 配置、策略或能力更新通知
- 多节点协作工作流中的阶段推进与状态广播
- 监控、审计、告警流转

### 与其他模块的区别

- `Messaging` 更偏点对点消息投递
- `RPC / Tasks` 更偏请求-响应和异步任务控制
- `PubSub` 更偏 topic 驱动、松耦合、多消费者分发

### 协议层建议关注点

- topic 命名规范
- subscribe / unsubscribe 语义
- wildcard 支持策略
- 是否要求 ack
- 是否支持 durable subscription
- 是否支持 replay 最近消息
- 是否要求分区内有序

### Subjects

| Subject | 说明 |
|---------|------|
| `clawsynapse.pubsub.subscribe` | 创建或更新订阅 |
| `clawsynapse.pubsub.unsubscribe` | 取消订阅 |
| `clawsynapse.pubsub.publish.<topic>` | 向 topic 发布消息 |
| `clawsynapse.pubsub.ack` | 确认消费消息 |

### Topic 命名建议

建议采用分层 topic：

```text
<domain>.<resource>.<event>
```

示例：

- `tasks.queue.created`
- `agents.lifecycle.started`
- `system.alert.raised`

建议按可见性划分：

- 公开 topic：适合监控、低敏事件
- 租户级 topic：仅同租户或同工作空间可见
- trusted topic：只有已建立 trust 的节点可订阅或发布

### `pubsub.subscribe`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 订阅请求 ID |
| `messageType` | `string` | 是 | 固定为 `pubsub.subscribe` |
| `nodeId` | `string` | 是 | 订阅方节点 ID |
| `topic` | `string` | 是 | 目标 topic |
| `consumerId` | `string` | 是 | 消费者标识 |
| `filter` | `object` | 否 | 订阅过滤条件 |
| `durable` | `boolean` | 否 | 是否持久订阅 |
| `replay` | `string` | 否 | `none`、`latest`、`since` |
| `replaySince` | `number` | 否 | 从某时间点回放 |
| `requiresAck` | `boolean` | 否 | 是否要求 ack |
| `ts` | `number` | 是 | 订阅时间 |
| `signature` | `string` | 否 | 可选签名 |

### `pubsub.unsubscribe`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 取消订阅请求 ID |
| `messageType` | `string` | 是 | 固定为 `pubsub.unsubscribe` |
| `nodeId` | `string` | 是 | 订阅方节点 ID |
| `topic` | `string` | 是 | 目标 topic |
| `consumerId` | `string` | 是 | 消费者标识 |
| `ts` | `number` | 是 | 请求时间 |
| `signature` | `string` | 否 | 可选签名 |

### `pubsub.publish.<topic>`

建议在 `Messaging Envelope` 之上发布，至少包含：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | `string` | 是 | Envelope ID |
| `type` | `string` | 是 | 固定为 `event.forward` 或业务事件类型 |
| `from` | `string` | 是 | 发布方节点 ID |
| `content` | `string` | 否 | 事件正文 |
| `metadata.topic` | `string` | 是 | 当前 topic |
| `metadata.partitionKey` | `string` | 否 | 分区键 |
| `metadata.requiresAck` | `boolean` | 否 | 是否需要消费确认 |
| `metadata.retentionMs` | `number` | 否 | 消息保留时长 |
| `ts` | `number` | 是 | 发布时间 |
| `sig` | `string` | 否 | 可选签名 |

### `pubsub.ack`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | ack 消息 ID |
| `messageType` | `string` | 是 | 固定为 `pubsub.ack` |
| `nodeId` | `string` | 是 | 消费方节点 ID |
| `consumerId` | `string` | 是 | 消费者标识 |
| `topic` | `string` | 是 | 对应 topic |
| `eventId` | `string` | 是 | 被确认的消息 ID |
| `ts` | `number` | 是 | ack 时间 |

### 语义约定

- `subscribe` 成功后，系统为该 `consumerId` 建立接收状态
- `durable = true` 时，系统应记录消费位点
- `replay = latest` 表示只接收当前最新快照后的消息
- `replay = since` 结合 `replaySince` 用于回放某时间点之后的消息
- `requiresAck = true` 时，消费方应发送 `pubsub.ack`
- 是否支持至少一次、至多一次或恰好一次投递，需要在具体实现中明确

## Data Transfer

### 定义

`Data Transfer` 用于传输较大的文本、二进制对象或文件，不建议把大 payload 直接塞进普通 Envelope 的 `content` 字段。

### 作用

- 传输文件或大体积结果
- 支持分片、重试、校验和完成确认
- 让跨节点协作可以交换 artifact，而不仅是短消息
- 降低大消息对普通消息通道的冲击

### 典型使用场景

- 传输日志包、压缩结果、模型产物
- 传输图片、音频、文档、zip 等文件
- 传输大型 JSON、数据集或 embedding 批次
- 工具调用结果过大，不能直接放进 RPC 返回体
- 任务完成后将结果文件回传给请求方

### 与其他模块的区别

- `Messaging` 适合小消息与轻量控制数据
- `RPC / Tasks` 适合请求、响应与任务控制信息
- `Data Transfer` 适合大对象、文件、二进制流与分片传输

### 协议层建议关注点

- transfer session / transfer id
- 文件元数据，例如 `name`、`size`、`mimeType`、`checksum`
- chunk 分片结构
- chunk ack / retry 机制
- transfer complete / abort 语义
- 是否支持 resume 与断点续传

### Subjects

| Subject | 说明 |
|---------|------|
| `clawsynapse.transfer.start` | 发起传输会话 |
| `clawsynapse.transfer.chunk` | 发送分片 |
| `clawsynapse.transfer.ack` | 确认分片或窗口 |
| `clawsynapse.transfer.complete` | 声明传输完成 |
| `clawsynapse.transfer.abort` | 中止传输 |

### `transfer.start`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 启动消息 ID |
| `messageType` | `string` | 是 | 固定为 `transfer.start` |
| `transferId` | `string` | 是 | 传输会话 ID |
| `from` | `string` | 是 | 发送方节点 |
| `to` | `string` | 是 | 接收方节点 |
| `name` | `string` | 否 | 文件名或对象名 |
| `size` | `number` | 是 | 总字节数 |
| `mimeType` | `string` | 否 | MIME 类型 |
| `checksum` | `string` | 否 | 完整内容校验和 |
| `chunkSize` | `number` | 是 | 分片大小 |
| `encoding` | `string` | 否 | 例如 `binary`、`base64`、`gzip+binary` |
| `resume` | `boolean` | 否 | 是否允许断点续传 |
| `ts` | `number` | 是 | 发起时间 |
| `signature` | `string` | 否 | 可选签名 |

### `transfer.chunk`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 分片消息 ID |
| `messageType` | `string` | 是 | 固定为 `transfer.chunk` |
| `transferId` | `string` | 是 | 传输会话 ID |
| `from` | `string` | 是 | 发送方节点 |
| `to` | `string` | 是 | 接收方节点 |
| `chunkIndex` | `number` | 是 | 分片序号，从 0 开始 |
| `offset` | `number` | 是 | 当前分片偏移 |
| `payload` | `string` | 是 | 分片内容，按约定编码 |
| `payloadSize` | `number` | 是 | 分片原始字节数 |
| `checksum` | `string` | 否 | 当前分片校验和 |
| `isLast` | `boolean` | 否 | 是否最后一个分片 |
| `ts` | `number` | 是 | 发送时间 |

### `transfer.ack`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | ack 消息 ID |
| `messageType` | `string` | 是 | 固定为 `transfer.ack` |
| `transferId` | `string` | 是 | 传输会话 ID |
| `from` | `string` | 是 | 接收方节点 |
| `to` | `string` | 是 | 发送方节点 |
| `ackedChunkIndex` | `number` | 否 | 已确认的分片序号 |
| `nextOffset` | `number` | 否 | 建议继续发送的偏移 |
| `windowSize` | `number` | 否 | 接收窗口 |
| `status` | `string` | 是 | `ok`、`retry`、`resume` |
| `ts` | `number` | 是 | ack 时间 |

### `transfer.complete`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 完成消息 ID |
| `messageType` | `string` | 是 | 固定为 `transfer.complete` |
| `transferId` | `string` | 是 | 传输会话 ID |
| `from` | `string` | 是 | 发送方节点 |
| `to` | `string` | 是 | 接收方节点 |
| `totalChunks` | `number` | 是 | 总分片数 |
| `size` | `number` | 是 | 总字节数 |
| `checksum` | `string` | 否 | 完整内容校验和 |
| `ts` | `number` | 是 | 完成时间 |

### `transfer.abort`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 中止消息 ID |
| `messageType` | `string` | 是 | 固定为 `transfer.abort` |
| `transferId` | `string` | 是 | 传输会话 ID |
| `from` | `string` | 是 | 发起中止的一方 |
| `to` | `string` | 是 | 对端 |
| `reason` | `string` | 否 | 中止原因 |
| `failedChunkIndex` | `number` | 否 | 失败分片 |
| `ts` | `number` | 是 | 中止时间 |

### 语义约定

- `transfer.start` 成功后，双方创建传输会话并预留接收状态
- 大文件不应直接通过普通 `Messaging Envelope.content` 承载
- 接收方可通过 `transfer.ack` 实现窗口控制与重试建议
- `resume = true` 时，接收方可返回 `status = resume` 和 `nextOffset`
- `transfer.complete` 表示发送方认为所有分片已发送完成，接收方仍应校验总大小与 checksum
- 任一方发现校验失败、权限不足、磁盘不足或会话过期时，可发送 `transfer.abort`

## Relay / Control

### `clawsynapse.control.trust.poll`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 轮询请求 ID |
| `messageType` | `string` | 是 | 固定为 `control.trust.poll` |
| `nodeId` | `string` | 是 | 当前节点 ID |
| `ts` | `number` | 是 | 请求时间 |
| `signature` | `string` | 是 | 节点签名 |

返回体建议包含：

- `pendingRequests`
- `pendingResponses`
- `nextCursor` 或 `drained`

### `clawsynapse.control.trust.respond`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 控制面响应 ID |
| `messageType` | `string` | 是 | 固定为 `control.trust.respond` |
| `nodeId` | `string` | 是 | 当前节点 ID |
| `requestId` | `string` | 是 | 对应 trust request |
| `decision` | `string` | 是 | `approve` 或 `reject` |
| `reason` | `string` | 否 | 审批备注 |
| `ts` | `number` | 是 | 响应时间 |
| `signature` | `string` | 是 | 节点签名 |

## 待补充模块

以下模块建议后续继续按相同格式补全：

- `Presence / Session`：会话建立、续租、断连恢复

## 待细化项

以下模块已完成基础协议定义，但仍建议继续细化高级语义：

- `PubSub`：filter 结构、wildcard 规则、durable 位点模型、投递语义、保序策略
- `Data Transfer`：chunk 校验算法、resume 冲突处理、窗口控制、限流、超时与并发分片策略

## 版本演进建议

- 所有新消息默认带 `protocolVersion`
- 破坏性变更通过新版本前缀或版本字段显式区分
- 同一阶段尽量保证字段向后兼容
- 在协议表中标记 `experimental` 字段，避免过早固化

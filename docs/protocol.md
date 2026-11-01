---
summary: "ClawSynapse 项目级协议：消息封套、签名规范、错误码与模块协议"
title: "ClawSynapse Protocol"
---

# ClawSynapse Protocol

最后更新：2026-10-20

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

本地产品模型区分宿主 Daemon 和长期协作身份 Bot。每个 Bot 是独立的网络参与者；本文沿用 wire v2 的 `node` / `nodeId` 术语，均指 Bot 的网络身份，不指 daemon 或机器。`peer` 是当前本地 Bot 视角的对端 Bot。Harness 是 Bot 绑定的执行引擎，其内部 Agent 和 Session 不是新的网络身份。

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

- `Discovery`：Bot 发现、announce、元数据暴露
- `Auth`：challenge-response 身份认证
- `Trust`：显式授权、审批、撤销
- `Relay / Control`：中继、轮询、控制面交互
- `RPC / Tasks`：任务请求、任务执行、响应返回
- `Events / PubSub`：事件广播、订阅、过滤

本文先定义公共规范，并完整列出 `Auth`、`Trust` 与 `Relay / Control` 的协议。其他模块后续按相同模式补齐。

## 模块索引

| 模块 | 主要内容 | 当前状态 |
|------|----------|----------|
| `Discovery` | Bot 发现、announce、depart、TTL | 已定义基础协议 |
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

- 使用小写英文、数字与 `-`，并以 `.` 分层
- 一级前缀固定为 `clawsynapse`
- 二级固定为模块名：`auth`、`trust`、`discovery`、`control`、`msg`、`events`、`pubsub`、`transfer`
- 三级为作用域（Bot、topic 或 global），四级及以下为动作

通用模式：

```text
clawsynapse.<module>.<scope>.<action>[.<subaction>]
```

语法（BNF）：

```text
subject        ::= "clawsynapse" "." module "." scope "." action [ "." subaction ]
module         ::= "auth" | "trust" | "discovery" | "control" | "msg" | "events" | "pubsub" | "transfer"
scope          ::= node-id | topic | "global" | "trust" | "auth"
action         ::= token
subaction      ::= token
token          ::= [a-z0-9-]+
node-id        ::= [a-z0-9-]{3,64}
topic          ::= [a-z0-9-]+("."[a-z0-9-]+){0,7}
```

约束：

- 不允许 `*`、`>`、空格或大写字符出现在协议定义的固定 subject 中
- `nodeId` 必须是由 Bot 身份公钥派生出的 subject-safe 标识，不能由外部随意指定
- `subject` 中的目标 Bot（若存在）必须与 payload 的 `to` 一致
- `messageType` 与 `subject` 的模块必须一致
- `subject` 必须参与签名，避免跨 subject 重放

示例：

```text
clawsynapse.auth.n1-11223344556677889900aabbccddeeff.challenge.request
clawsynapse.trust.n1-11223344556677889900aabbccddeeff.response
clawsynapse.msg.n1-11223344556677889900aabbccddeeff.inbox
clawsynapse.control.trust.poll
```

身份派生规则：

- Bot 长期身份由 Ed25519 公钥确定
- `did = did:key:z...`
- `nodeId = "n1-" + hex(sha256(did)[:16])`
- 收到 discovery announce 时，接收方必须校验 `publicKey -> did -> nodeId` 三者一致

本地管理模型的 `botId` 与上述 `nodeId` 值完全相同，不能独立赋值。改公开名、模型、Harness Binding、停止/启动及完整迁移不改变身份。协议编码 seam 仍写入 `nodeId`、`from`、`to`；不在网络消息中增加 `botId` 别名，不改 subject、签名字节或按身份派生的 durable consumer。每 Bot 独立 inbox 和 consumer，仍可共享同一个 stream；旧 wire v2 对端无需为本地多 Bot 管理升级。

### `messageType` 约定

- 使用 `<module>.<action>[.<subaction>]` 形式
- 与 subject 语义保持一致
- `messageType` 仅表达语义，不包含路由范围（Bot、topic）

示例：

- `auth.challenge.request`
- `auth.challenge.response`
- `trust.request`
- `trust.response`
- `control.trust.poll`
- `control.trust.response`

## 公共消息封套

所有控制消息建议共享以下基础字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 消息唯一 ID，用于去重与审计 |
| `messageType` | `string` | 是 | 逻辑消息类型，例如 `trust.request` |
| `from` | `string` | 视场景 | 发起方 nodeId（由 DID 派生） |
| `to` | `string` | 视场景 | 目标 nodeId（由 DID 派生） |
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

Messaging 与 Discovery 使用 `protocolVersion: "v2"`，不接收旧版未完整签名消息。升级须协调所有 Bot；没有自动回退或双协议兼容层。Auth 的 challenge proof 与 Trust 的控制消息沿用各自握手／签名契约，不应套用下述数据面编码。

### v2 签名输入

Ed25519 签名的原文是下列 JSON 对象的 UTF-8 字节，而不是字段换行拼接：

```json
{
  "domain": "clawsynapse/wire/v2",
  "subject": "clawsynapse.msg.<targetNodeId>.inbox",
  "message": {}
}
```

`message` 放入完整消息对象，只移除顶层 `sig`／`signature`；路由、Agent、会话、正文、metadata、版本、有效期与关联字段全部参与签名。外层字段顺序固定为 `domain`、`subject`、`message`，内部对象按 Go `encoding/json` 的键排序与转义规则编码，包括 HTML 与 U+2028/U+2029 转义；数组保持顺序。数字使用 `json.Number` 保留 JSON 数字字面量，不允许接收端先转为 float64 再验签。这不是 RFC 8785/JCS，其他语言实现须按相同规则生成字节。

这些 v2 接收消息拒绝未知结构字段、任意深度的重复键、尾随 JSON、无效 UTF-8、超过 32 层嵌套及超限数据。metadata／input 内允许业务键，但不允许重复键。验签后仍需检查权限、时效与去重。

### 安全边界

- 签名证明声明来自持钥 Bot，不证明业务身份、设备审批或标签内容真实。
- 消息 journal、outbox、幂等键与接收游标按 Bot 持久化；发现目录仍为运行态。不能将进程内重放窗口或发现缓存等同于完整业务恢复保证。
- 传输保密依赖 NATS TLS 与服务端 account/subject 权限，不宣称已经提供端到端正文加密或密钥轮换。


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
    "peer": "n1-11223344556677889900aabbccddeeff",
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
| `protocol.invalid_subject` | subject 不符合命名规范 |
| `protocol.subject_target_mismatch` | subject 目标与 payload.to 不一致 |
| `protocol.module_mismatch` | subject 与 messageType 模块不一致 |
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
| 点对点消息 | `clawsynapse.msg.<targetNodeId>.inbox` | 目标 Bot 收件箱 |
| 事件消息 | `clawsynapse.events.<nodeId>.<eventName>` | 生命周期或系统事件 |
| Bot 发现 | `clawsynapse.discovery.global.announce` | Bot 注册与心跳 |
| Bot 发现 | `clawsynapse.discovery.global.depart` | Bot 主动下线 |
| 中继控制 | `clawsynapse.control.trust.poll` | 拉取 pending trust |
| 中继控制 | `clawsynapse.control.trust.response` | 回写审批结果 |
| 中继控制 | `clawsynapse.control.auth.poll` | 拉取待处理认证事件 |
| PubSub | `clawsynapse.pubsub.global.subscribe` | 创建或更新订阅 |
| PubSub | `clawsynapse.pubsub.global.unsubscribe` | 取消订阅 |
| PubSub | `clawsynapse.pubsub.<topic>.publish` | 向 topic 发布消息 |
| PubSub | `clawsynapse.pubsub.<topic>.ack` | 确认消费消息 |
| Data Transfer | `clawsynapse.transfer.<targetNodeId>.start` | 发起传输会话 |
| Data Transfer | `clawsynapse.transfer.<targetNodeId>.chunk` | 发送分片 |
| Data Transfer | `clawsynapse.transfer.<targetNodeId>.ack` | 确认分片或窗口 |
| Data Transfer | `clawsynapse.transfer.<targetNodeId>.complete` | 声明传输完成 |
| Data Transfer | `clawsynapse.transfer.<targetNodeId>.abort` | 中止传输 |

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

`Discovery` 负责 Bot 出现、续租、下线与最小必要元数据传播。它不直接授予信任，也不代替后续认证。

### Subjects

| Subject | 说明 |
|---------|------|
| `clawsynapse.discovery.global.announce` | Bot 注册、心跳、元数据刷新 |
| `clawsynapse.discovery.global.depart` | Bot 主动下线通知 |

### `discovery.announce`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `protocolVersion` | `string` | 是 | 固定为 `v2` |
| `messageId` | `string` | 是 | announce 消息 ID |
| `messageType` | `string` | 是 | 固定为 `discovery.announce` |
| `nodeId` | `string` | 是 | Bot 唯一标识 |
| `did` | `string` | 是 | 与公钥及 Bot ID 派生关系一致的 DID |
| `version` | `string` | 是 | Agent 产品版本；未配置 `agentVersion` 时使用 daemon 构建版本 |
| `agentProduct` | `string` | 是 | 产品标识 |
| `displayName` | `string` | 否 | Bot 的人类可读公开名，≤128 字节，不允许控制字符；允许重名，不替代 Bot ID |
| `labels` | `object` | 否 | Bot 属性；值为字符串或字符串数组。保留键 `caps` 必须是字符串数组，表示自报能力集合。限制：≤32 个键、键 ≤64 字节、单个值 ≤256 字节、数组 ≤32 个元素、整体 ≤4096 字节；发送方在签名前规范化（数组去重、排序，空元素丢弃） |
| `inbox` | `string` | 是 | 默认收件箱 subject |
| `publicKey` | `string` | 是 | Bot 公钥 |
| `ts` | `number` | 是 | 发布时间 |
| `ttlMs` | `number` | 是 | Bot 租约时长 |
| `metadata` | `object` | 否 | 有界扩展声明；当前不导入 peer 业务属性 |
| `signature` | `string` | 是 | 全字段及 subject 绑定的 Ed25519 签名 |

### `discovery.depart`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `protocolVersion` | `string` | 是 | 固定为 `v2` |
| `messageId` | `string` | 是 | depart 消息 ID |
| `messageType` | `string` | 是 | 固定为 `discovery.depart` |
| `nodeId` | `string` | 是 | 下线 Bot ID |
| `reason` | `string` | 否 | 下线原因 |
| `ts` | `number` | 是 | 下线时间 |
| `signature` | `string` | 是 | 使用已知 Bot 公钥验证的完整声明签名 |

### 处理约定

- announce 的签名覆盖整个声明（含 `displayName` 与 `labels`）；发送方在签名前对 labels 做规范化。因此远端名称、能力与场景标签只能来自签名、未过期且未重放的声明。修改任意字段都会使签名失效。
- `displayName`/`labels` 取代了旧的 `capabilities` 字段。接收端严格解码（未知字段拒绝）且签名绑定全部字段，新旧版本 announce 互不兼容：混部集群中的旧版 Bot 拒绝新 announce，新版 Bot 同样拒绝旧 announce，双方失败关闭、不会互相发现。升级时必须整批切换。
- announce/depart 均检查真实 subject、消息类型、派生身份、签名、时间与消息 ID；`open` 模式不跳过签名。
- 发现消息最多 32 KiB，metadata 最多 8 KiB，TTL 最大 2 分钟，未来时间偏差最大 30 秒；无效声明被拒绝。
- `lastSeenMs` 使用本地接收时间，不信任远端时间戳延长存活。
- 已知 Bot 的有效 depart 才能驱逐；保留有界的新鲜度／重放记录，旧 announce 不能立即复活已下线 Bot。
- 认证和信任状态由本地维护，不受声明覆盖；`labels.caps` 是自报能力，不授予权限。注意这与 trust request 的 `capabilities`（申请访问的能力范围）是两个不同概念。

## Messaging / Events

### 设计范围

`Messaging` 定义业务消息封套与点对点收件箱语义，`Events` 定义系统事件的广播格式。

### Subjects

| Subject | 说明 |
|---------|------|
| `clawsynapse.msg.<targetNodeId>.inbox` | 点对点消息收件箱 |
| `clawsynapse.pubsub.<topic>.publish` | 面向 topic 的广播 |
| `clawsynapse.events.<nodeId>.<eventName>` | 生命周期或业务事件 |

### Envelope

业务消息使用 UTF-8 JSON，每个对象对应一条 NATS 消息，不在一条消息中封装持续增长的 JSONL。完整消息最大 256 KiB。

```json
{
  "id": "message-id",
  "type": "task.request",
  "from": "n1-sender",
  "to": "n1-receiver",
  "payload": {
    "instruction": "分析日志",
    "input": {"log": "database timeout"}
  },
  "sessionKey": "session-17",
  "ts": 1792454400000,
  "expiresAt": 1792454460000,
  "protocolVersion": "v2",
  "sig": "<signature>"
}
```

Bot ID 与签名在此仅作结构示意，不能直接用于实际验签。

### `Envelope.type` 预留值

可投递类型（会被转发给本地 Agent handler，由 `deliverablePrefixes` 配置项控制，默认 `chat,task`）：

- `chat.message` — 文本输入，放入 `payload.text`
- `task.request`、`task.assign` — 结构化任务，使用 `payload.instruction/input`

非命令类型（持久存入 inbox，不触发 LLM）：

- `event.forward` — 事件转发
- `run.started`、`run.output.delta`、`run.completed`、`run.failed`、`run.accepted` — 执行反馈，不进入 LLM；Webhook 可消费，但不产生反馈回复
- `chat.context` — 经授权共享的聊天历史／群成员观察数据；仅投递 Webhook 数据消费者，与 deliverablePrefixes 无关；不生成 run.started/output/completed，不对观察再发反馈，native Adapter 不执行

可通过配置 `deliverablePrefixes` 扩展可投递前缀，详见环境变量 `DELIVERABLE_PREFIXES`。

### 字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| `id` | 是 | 消息唯一标识，用于去重 |
| `type` | 是 | 消息类型 |
| `agentId` | 否 | 本地授权列表内的 Agent；Codex 不支持命名 Agent 路由 |
| `from` | 是 | 发送方 Bot ID |
| `to` | 是 | 精确目标 Bot ID，必须与 subject 一致 |
| `payload` | 是 | 结构化正文；支持 text、instruction、input，以及执行事件的 channel、code、stage、indeterminate、adapterRunId |
| `sessionKey` | 否 | 会话标识 |
| `ts` | 是 | 发送时间戳 |
| `expiresAt` | 是 | Unix 毫秒有效期；默认 10 分钟，最大 ts + 10 分钟，排队后执行前再次检查 |
| `requestId` | 执行事件必填 | 原请求消息 ID |
| `runId` | 执行事件必填 | 接收 Bot 生成的一次执行 ID，不等于产品会话 ID |
| `seq` | 执行事件必填 | 同一执行中从 1 开始递增的序号 |
| `sig` | 是 | 完整 v2 消息签名 |
| `metadata` | 否 | 扩展字段 |
| `protocolVersion` | 是 | 固定为 `v2` |

### 处理约定

- 使用精确 inbox subject，按原请求 ID 关联执行事件；终态不重复携带已通过 delta 发出的全文。
- 正常模式要求双方认证与信任；`open` 仅用于非生产环境，仍要求已发现身份和有效签名。
- 8 个串行工作分片，按发送方、会话及可选本地 agentId 排序；等待项持久化在本地 journal，不依赖内存队列容量，也不为每条消息启动 goroutine。执行前重新检查认证与信任；未知/尚未恢复认证的 Bot 等待，明确撤销的 Bot 不会执行。
- 通用任务的 LLM 会话在接收 Bot、Binding 修订、发送方与原会话作用域内隔离；DSH 聊天以 `metadata.dsynapseChat.chatId` 建立 Bot 级单会话，聊天参与者只有 Bot。`agentId` 是 Binding 内允许列表约束的提示，不选择另一个 Bot/Harness。Webhook 保留会话键与事件关联字段。
- `run.accepted` 只表示异步接纳，`payload.adapterRunId` 可用于业务端回查；`run.completed` 也不等于设备物理动作完成。
- 结果未知时 `run.failed.payload.indeterminate=true`，不能据此自动重跑。
- 点对点收件箱使用文件存储的共享 JetStream `CLAWSYNAPSE_MESSAGES` stream，subject 为 `clawsynapse.msg.*.inbox`，Limits retention 默认 7 天。初始化不改写现有 stream 的其他 subject，也不清除其他 Bot 消息；不兼容的现有 stream 失败关闭。
- 每 Bot 使用固定 durable pull consumer、explicit ACK。有效签名消息和待投递工作在本地 fsync 后才 ACK；重投按发送 Bot+消息 ID 去重。签名历史消息可归档，过期命令生成 execution.expired，而历史反馈仅作为数据消费。
- 本地 publication/outbox、幂等键、接收游标、admitted work 均持久化。发布接口成功只表示 JetStream durable ACK，不回退 Core NATS。输出事件先与工作序号原子写入本地 outbox，再按执行顺序重试发送；原始目标 agentId 与 metadata.dsynapseChat 的 chatId/messageId 保留，历史上下文不重复到每个 delta。
- 进入 Adapter 前先持久标记 admitted。重启发现未终结 admitted command 时生成 `run.failed`、`indeterminate=true`，不盲目重放副作用。仅能证明 HTTP dial 未建立的 Webhook 失败可按同一消息 ID 重试；反馈是非执行数据，可按同一 ID 重投。不能据此宣称外部系统副作用 exactly-once。
- `GET /v2/bots/{botId}/messages?after=N&limit=N` 提供该 Bot 的本地持久接收游标；`GET /v2/bots/{botId}/requests/{messageId}` 提供发布与关联执行事件查询，详见本地 API 文档。发送目标在本地 API 中为 `targetBot`，编码为 wire v2 的 `to`；旧 `/v1` 单身份入口已移除。

## PubSub

### 定义

`PubSub` 是发布/订阅模型，用于把消息发送到一个 topic，并由一个或多个订阅方按规则接收。它关注的是“谁订阅了某类消息”，而不是“把消息直接投给哪个固定 Bot inbox”。

### 作用

- 支持一对多广播
- 解耦消息生产者与消费者
- 让多个 agent 共享同一类事件流
- 作为任务分发、系统事件和编排通知的基础能力

### 典型使用场景

- 多个 worker 订阅同一个任务 topic，等待新任务广播
- 系统事件总线，例如 `task.completed`、`agent.started`、`peer.departed`
- 配置、策略或能力更新通知
- 多 Bot 协作工作流中的阶段推进与状态广播
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
| `clawsynapse.pubsub.global.subscribe` | 创建或更新订阅 |
| `clawsynapse.pubsub.global.unsubscribe` | 取消订阅 |
| `clawsynapse.pubsub.<topic>.publish` | 向 topic 发布消息 |
| `clawsynapse.pubsub.<topic>.ack` | 确认消费消息 |

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
- trusted topic：只有已建立 trust 的 Bot 可订阅或发布

### `pubsub.subscribe`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 订阅请求 ID |
| `messageType` | `string` | 是 | 固定为 `pubsub.subscribe` |
| `nodeId` | `string` | 是 | 订阅方 Bot ID |
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
| `nodeId` | `string` | 是 | 订阅方 Bot ID |
| `topic` | `string` | 是 | 目标 topic |
| `consumerId` | `string` | 是 | 消费者标识 |
| `ts` | `number` | 是 | 请求时间 |
| `signature` | `string` | 否 | 可选签名 |

### `pubsub.<topic>.publish`

建议在 `Messaging Envelope` 之上发布，至少包含：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | `string` | 是 | Envelope ID |
| `type` | `string` | 是 | 固定为 `event.forward` 或业务事件类型 |
| `from` | `string` | 是 | 发布方 Bot ID |
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
| `nodeId` | `string` | 是 | 消费方 Bot ID |
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
- 让跨 Bot 协作可以交换 artifact，而不仅是短消息
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
- 文件元数据，例如 `name`、`size`、`mimeType`、`checksum`、`metadata`（业务扩展）
- chunk 分片结构
- chunk ack / retry 机制
- transfer complete / abort 语义
- 是否支持 resume 与断点续传

### Subjects

| Subject | 说明 |
|---------|------|
| `clawsynapse.transfer.<targetNodeId>.start` | 发起传输会话 |
| `clawsynapse.transfer.<targetNodeId>.chunk` | 发送分片 |
| `clawsynapse.transfer.<targetNodeId>.ack` | 确认分片或窗口 |
| `clawsynapse.transfer.<targetNodeId>.complete` | 声明传输完成 |
| `clawsynapse.transfer.<targetNodeId>.abort` | 中止传输 |

### `transfer.start`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 启动消息 ID |
| `messageType` | `string` | 是 | 固定为 `transfer.start` |
| `transferId` | `string` | 是 | 传输会话 ID |
| `from` | `string` | 是 | 发送方 Bot |
| `to` | `string` | 是 | 接收方 Bot |
| `name` | `string` | 否 | 文件名或对象名 |
| `size` | `number` | 是 | 总字节数 |
| `mimeType` | `string` | 否 | MIME 类型 |
| `checksum` | `string` | 否 | 完整内容校验和 |
| `chunkSize` | `number` | 是 | 分片大小 |
| `encoding` | `string` | 否 | 例如 `binary`、`base64`、`gzip+binary` |
| `resume` | `boolean` | 否 | 是否允许断点续传 |
| `metadata` | `object` | 否 | 业务扩展元数据，透传给接收方（如 `taskId`、`todoId`） |
| `ts` | `number` | 是 | 发起时间 |
| `signature` | `string` | 否 | 可选签名 |

### `transfer.chunk`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 分片消息 ID |
| `messageType` | `string` | 是 | 固定为 `transfer.chunk` |
| `transferId` | `string` | 是 | 传输会话 ID |
| `from` | `string` | 是 | 发送方 Bot |
| `to` | `string` | 是 | 接收方 Bot |
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
| `from` | `string` | 是 | 接收方 Bot |
| `to` | `string` | 是 | 发送方 Bot |
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
| `from` | `string` | 是 | 发送方 Bot |
| `to` | `string` | 是 | 接收方 Bot |
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
| `nodeId` | `string` | 是 | 当前 Bot ID |
| `ts` | `number` | 是 | 请求时间 |
| `signature` | `string` | 是 | Bot 签名 |

返回体建议包含：

- `pendingRequests`
- `pendingResponses`
- `nextCursor` 或 `drained`

### `clawsynapse.control.trust.response`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 控制面响应 ID |
| `messageType` | `string` | 是 | 固定为 `control.trust.response` |
| `nodeId` | `string` | 是 | 当前 Bot ID |
| `requestId` | `string` | 是 | 对应 trust request |
| `decision` | `string` | 是 | `approve` 或 `reject` |
| `reason` | `string` | 否 | 审批备注 |
| `ts` | `number` | 是 | 响应时间 |
| `signature` | `string` | 是 | Bot 签名 |

### `clawsynapse.control.auth.poll`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `messageId` | `string` | 是 | 轮询请求 ID |
| `messageType` | `string` | 是 | 固定为 `control.auth.poll` |
| `nodeId` | `string` | 是 | 当前 Bot ID |
| `ts` | `number` | 是 | 请求时间 |
| `signature` | `string` | 是 | Bot 签名 |

返回体建议包含：

- `pendingChallenges`
- `pendingResponses`
- `nextCursor` 或 `drained`

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

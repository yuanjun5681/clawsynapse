---
summary: "ClawSynapse 消息与协议：subject、Envelope、发现与请求回复"
title: "ClawSynapse Messaging"
---

# ClawSynapse Messaging

最后更新：2026-03-12

## subject 约定

### 点对点消息

```text
clawsynapse.agent.<targetNodeId>.inbox
```

### 广播

```text
clawsynapse.broadcast.<topic>
```

### 事件转发

```text
clawsynapse.events.<nodeId>.<eventName>
```

该 subject 用于将本地 Agent 生命周期事件转发到 NATS，供监控、审计或跨节点协调使用。

### 节点发现

```text
clawsynapse.discovery.announce
clawsynapse.discovery.depart
```

### 认证握手

```text
clawsynapse.auth.<targetNodeId>.challenge.request
clawsynapse.auth.<targetNodeId>.challenge.response
clawsynapse.auth.<targetNodeId>.challenge.ack
```

## Envelope

所有消息载荷使用 UTF-8 JSON 编码。

```go
type Envelope struct {
    ID         string         `json:"id"`
    From       string         `json:"from"`
    To         string         `json:"to,omitempty"`
    Type       string         `json:"type"`
    Content    string         `json:"content"`
    SessionKey string         `json:"sessionKey,omitempty"`
    ReplyTo    string         `json:"replyTo,omitempty"`
    Ts         int64          `json:"ts"`
    Sig        string         `json:"sig,omitempty"`
    Metadata   map[string]any `json:"metadata,omitempty"`
}
```

预留的 `type`：

- `chat.message`
- `chat.reply`
- `event.forward`
- `auth.challenge.request`
- `auth.challenge.response`
- `auth.challenge.ack`

Envelope 包含 `id` 和 `type`，用于幂等处理和后续扩展。

### 点对点消息示例

```json
{
  "id": "msg_01hrxyz",
  "from": "node-alpha",
  "to": "node-beta",
  "type": "chat.message",
  "content": "来自 Agent A 的消息",
  "sessionKey": "nats:node-alpha:node-beta",
  "replyTo": "clawsynapse.agent.node-alpha.inbox",
  "ts": 1741680000000,
  "sig": "base64url-ed25519-signature",
  "metadata": {}
}
```

字段说明：

| 字段 | 必填 | 说明 |
|------|------|------|
| `id` | 是 | 消息唯一标识，用于去重 |
| `from` | 是 | 发送方节点 ID |
| `to` | 否 | 目标节点 ID |
| `type` | 是 | 消息类型 |
| `content` | 是 | 消息正文 |
| `sessionKey` | 否 | 接收方 Agent 使用的会话标识 |
| `replyTo` | 否 | 回复地址 subject |
| `ts` | 是 | 发送时间戳（毫秒） |
| `sig` | 否 | 消息签名 |
| `metadata` | 否 | 任意扩展字段 |

## 节点发现

每个 `clawsynapsed` 启动后：

1. 加载或生成 Ed25519 密钥对
2. 连接 NATS
3. 订阅发现相关 subject
4. 发布自身 announce
5. 周期性发送心跳 announce
6. 维护本地 peer 表和 TTL 驱逐

### 注册载荷示例

```json
{
  "nodeId": "node-alpha",
  "version": "2026.3.9",
  "agentProduct": "openclaw",
  "capabilities": ["chat", "tools", "voice"],
  "inbox": "clawsynapse.agent.node-alpha.inbox",
  "publicKey": "base64url-ed25519-public-key",
  "ts": 1741680000000,
  "ttlMs": 30000,
  "metadata": {
    "hostname": "macbook-pro",
    "platform": "darwin",
    "channels": ["telegram", "discord"]
  }
}
```

字段说明：

| 字段 | 必填 | 说明 |
|------|------|------|
| `nodeId` | 是 | 节点唯一标识 |
| `version` | 是 | Agent 产品版本号 |
| `agentProduct` | 是 | Agent 产品标识 |
| `capabilities` | 是 | 节点能力列表 |
| `inbox` | 是 | 接收消息的 NATS subject |
| `publicKey` | 是 | 节点公钥 |
| `ts` | 是 | 注册时间戳 |
| `ttlMs` | 是 | 存活时间 |
| `metadata` | 否 | 扩展信息 |

### 下线通知示例

```json
{
  "nodeId": "node-alpha",
  "ts": 1741680030000,
  "reason": "shutdown"
}
```

其他节点收到 `depart` 后立即驱逐该节点；未收到 `depart` 时，由 TTL 驱逐处理异常关闭场景。

### 生命周期

```text
clawsynapsed 启动
    │
    ├─ 1. 生成或加载 Ed25519 密钥对
    ├─ 2. 连接 NATS
    ├─ 3. 订阅 clawsynapse.discovery.announce
    ├─ 4. 发布自身注册信息
    ├─ 5. 启动心跳定时器
    │
    ├─ 收到其他节点注册信息
    │    ├─ 更新本地节点目录
    │    └─ 设置 TTL 驱逐时间
    │
    ├─ TTL 到期
    │    └─ 驱逐 peer
    │
    └─ 关闭时发布 depart
```

## 数据流

### 发送消息

1. 本地 Agent 调用 `clawsynapsed` 的 `publish` API
2. `clawsynapsed` 查询 peer 目录，定位目标节点 inbox
3. 目标节点未认证时先执行握手
4. 对消息签名后发布到目标 subject
5. 对端 `clawsynapsed` 收到消息并验签
6. 对端通过本地 Adapter 投递给 Agent

### 同步请求/回复

1. 本地 Agent 调用 `request` API
2. 消息中携带 `replyTo`
3. 远端 Agent 处理完成后，由远端 `clawsynapsed` 回发 `chat.reply`
4. 本地 `clawsynapsed` 等待并返回结果

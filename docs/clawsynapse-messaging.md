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

## 节点发现

每个 `clawsynapsed` 启动后：

1. 加载或生成 Ed25519 密钥对
2. 连接 NATS
3. 订阅发现相关 subject
4. 发布自身 announce
5. 周期性发送心跳 announce
6. 维护本地 peer 表和 TTL 驱逐

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

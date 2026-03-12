---
summary: "基于 NATS 的跨 Agent 通信：Sidecar 服务、节点发现与身份认证设计"
read_when:
  - 连接多个 OpenClaw 网关或异构 Agent 产品
  - 搭建分布式 Agent 间消息通信
  - 实现跨网关的节点自动发现
title: "NATS Bridge"
---

# NATS Bridge（跨 Agent 通信协议）

最后更新：2026-03-11

## 概述

NATS Bridge 使**多个 Agent 节点**（OpenClaw、LangChain、AutoGPT 或任何自研
Agent 产品）通过共享的 [NATS](https://nats.io) 消息总线实现互相通信。

核心设计目标：**Sidecar 服务完全独立于任何 Agent 产品**。它只依赖两个公开协议
（NATS 客户端 + Agent 网关的 API），不绑定任何 SDK 或内部实现。

### 设计原则

1. **独立 Sidecar 服务** -- 一个独立的长驻进程（Node.js / Python / Go 均可），
   负责 NATS 订阅、节点发现、消息转发。它通过 Agent 产品的公开 API（如 OpenClaw
   的 [WebSocket 协议](/gateway/protocol) `chat.send`）与本地 Agent 交互，不依赖
   任何 Agent SDK。
2. **Skill 发布出站消息** -- Agent 需要主动发消息时，调用一个轻量 Skill
   （`nats_publish`）连接 NATS 发布消息，无状态、短连接。
3. **节点自动发现** -- 所有 Sidecar 在启动时自动注册，定期心跳，互相维护在线
   节点目录。
4. **Agent 间身份认证** -- 基于 Ed25519 密钥对的挑战-响应握手，确保节点身份可信
   且消息不可伪造。
5. **协议无关** -- NATS subject 载荷是简单的 JSON，任何语言/框架都能生产和消费。

## 整体架构

```
  Agent A (OpenClaw)          NATS Server          Agent B (任意 Agent 产品)
 ┌───────────────────┐                            ┌───────────────────┐
 │ Agent 网关        │                            │ Agent 网关        │
 │ (WS / HTTP API)   │                            │ (WS / HTTP API)   │
 │        ▲          │                            │        ▲          │
 │        │ chat.send│                            │        │ 投递 API │
 │        │          │                            │        │          │
 │ ┌──────┴────────┐ │    ┌──────────────────┐   │ ┌──────┴────────┐ │
 │ │  Sidecar A    │◄├────┤                  ├───►│ │  Sidecar B    │ │
 │ │  (独立进程)    │ │    │   NATS Server    │   │ │  (独立进程)    │ │
 │ └───────────────┘ │    │                  │   │ └───────────────┘ │
 │                   │    └──────────────────┘   │                   │
 │ Agent 调用 Skill  │             ▲              │ Agent 调用 Skill  │
 │ nats_publish ─────├─────────────┘              │ nats_publish ─────│
 └───────────────────┘                            └───────────────────┘
```

**关键点：Sidecar 是独立进程，不是 Agent 的插件或扩展。** 它可以用任何语言实现，
只要能连接 NATS 和调用本地 Agent 的公开 API。更换 Agent 产品时，只需修改
Sidecar 的 API 适配层（如将 `chat.send` 替换为其他 Agent 的投递接口），NATS 侧
协议完全不变。

### 数据流（A 发送消息给 B）

1. Agent A 调用 `nats_publish` Skill，指定目标节点和消息内容。
2. Skill 连接 NATS，发布到 `openclaw.agent.<nodeB>.inbox`，断开。
3. Sidecar B 从 NATS 订阅收到消息。
4. Sidecar B 调用本地 Agent 网关的 API（如 `chat.send`）投递消息。
5. Agent B 处理消息，回复通过正常通道送达。

## NATS Subject 规范

所有载荷为 UTF-8 编码的 JSON。

### 点对点消息

```
openclaw.agent.<nodeId>.inbox
```

载荷：

```json
{
  "from": "node-alpha",
  "content": "来自 Agent A 的消息",
  "sessionKey": "nats:node-alpha:node-beta",
  "replyTo": "openclaw.agent.node-alpha.inbox",
  "sig": "base64url-ed25519-signature",
  "metadata": {}
}
```

| 字段 | 必填 | 说明 |
|------|------|------|
| `from` | 是 | 发送方节点 ID |
| `content` | 是 | 消息正文 |
| `sessionKey` | 否 | 接收方 Agent 使用的会话标识（省略则自动生成） |
| `replyTo` | 否 | 回复地址 subject（用于 request-reply 模式） |
| `sig` | 否 | 消息签名（启用身份认证时必填，见下文） |
| `metadata` | 否 | 任意键值对 |

### 广播

```
openclaw.broadcast.<topic>
```

载荷结构同上。适用于公告、共享上下文更新或任务分发。

### 事件转发（可选）

```
openclaw.events.<nodeId>.<eventName>
```

将 Agent 网关的生命周期事件（agent_end、message_sent 等）转发到 NATS，
用于监控或跨节点协调。由 Sidecar 可选配置。

## 节点自动发现

每个 Sidecar 参与基于 NATS 的节点发现协议，无需静态配置即可发现所有在线节点。

### 发现 Subject

```
openclaw.discovery.announce
```

### 注册载荷

```json
{
  "nodeId": "node-alpha",
  "version": "2026.3.9",
  "agentProduct": "openclaw",
  "capabilities": ["chat", "tools", "voice"],
  "inbox": "openclaw.agent.node-alpha.inbox",
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

| 字段 | 必填 | 说明 |
|------|------|------|
| `nodeId` | 是 | 节点唯一标识 |
| `version` | 是 | Agent 产品版本号 |
| `agentProduct` | 是 | Agent 产品标识（`openclaw`、`langchain`、`autogpt`、自定义） |
| `capabilities` | 是 | 节点能力列表 |
| `inbox` | 是 | 接收消息的 NATS subject |
| `publicKey` | 是 | 节点的 Ed25519 公钥（base64url 编码），用于身份认证 |
| `ts` | 是 | Unix 时间戳（毫秒） |
| `ttlMs` | 是 | 存活时间；其他节点在 `ts + ttlMs` 后驱逐此条目 |
| `metadata` | 否 | 扩展信息 |

### 生命周期

```
Sidecar 启动
    │
    ├─ 1. 生成或加载 Ed25519 密钥对
    ├─ 2. 连接 NATS
    ├─ 3. 订阅 openclaw.discovery.announce
    ├─ 4. 发布自身注册信息（立即）
    ├─ 5. 启动心跳定时器（默认每 15 秒）
    │      └─ 重新发布注册信息（更新 ts）
    │
    │  ┌─ 收到其他节点的注册信息时：
    │  │    ├─ 更新或插入到本地节点目录
    │  │    └─ 设置驱逐定时器 = ts + ttlMs
    │  │
    │  ├─ 驱逐定时器触发（未收到新心跳）：
    │  │    └─ 从目录中移除该节点
    │  │
    └─ Sidecar 关闭时：
         └─ 发布下线通知（可选）
```

### 下线通知（可选）

```
openclaw.discovery.depart
```

```json
{
  "nodeId": "node-alpha",
  "ts": 1741680030000,
  "reason": "shutdown"
}
```

其他节点收到后立即驱逐该节点。这是尽力而为的机制；TTL 驱逐处理非正常关闭。

### 节点目录

每个 Sidecar 维护一个内存中的节点映射表：

```typescript
type PeerEntry = {
  nodeId: string;
  version: string;
  agentProduct: string;
  capabilities: string[];
  inbox: string;
  publicKey: string;
  ts: number;
  ttlMs: number;
  metadata?: Record<string, unknown>;
};

// Map<nodeId, PeerEntry>
const peers = new Map<string, PeerEntry>();
```

Agent 可通过 `nats_peers` Skill 查询当前在线节点列表。

## 身份认证

Agent 间通信需要确保：消息来源可信（不可伪造），通信双方身份互认。
采用 **Ed25519 密钥对 + 挑战-响应握手** 方案。

### 密钥管理

每个 Sidecar 节点在首次启动时生成一对 Ed25519 密钥：

```
~/.nats-bridge/
├── identity.key         # 私钥（Ed25519，64 字节，权限 0600）
├── identity.pub         # 公钥（Ed25519，32 字节）
└── peers/               # 已信任节点的公钥缓存
    ├── node-beta.pub
    └── node-gamma.pub
```

- **私钥** 仅存在于本地文件系统，永远不通过 NATS 传输。
- **公钥** 通过 `openclaw.discovery.announce` 广播，所有节点自动收集。
- 节点目录中记录每个 peer 的 `publicKey`。

### 信任模型

提供三种信任级别，按需选择：

| 级别 | 说明 | 适用场景 |
|------|------|----------|
| **open** | 不验证签名，接受所有消息 | 本地开发、内网测试 |
| **tofu** | Trust-On-First-Use：首次见到的公钥自动信任并缓存，后续严格验证 | 小团队、可信网络 |
| **explicit** | 仅接受预先配置的公钥列表中的节点 | 生产环境、跨网络部署 |

配置：

```bash
# Sidecar 配置
TRUST_MODE=tofu          # open | tofu | explicit
TRUSTED_KEYS_DIR=~/.nats-bridge/peers/
```

### 握手流程（挑战-响应）

两个节点在首次通信时执行一次双向握手，互相验证身份：

```
Node A                          NATS                         Node B
  │                                                            │
  │  1. challenge.request                                      │
  │    { from: "A", publicKey: "pkA", nonce: "rA" }           │
  ├──────────────────────────────►─────────────────────────────►│
  │                                                            │
  │  2. challenge.response                                     │
  │    { from: "B", publicKey: "pkB", nonce: "rB",            │
  │      proof: sign(skB, "rA|B|A") }                         │
  │◄──────────────────────────────◄─────────────────────────────┤
  │                                                            │
  │  3. Node A 验证 proof：                                     │
  │    verify(pkB, "rA|B|A", proof) == true                    │
  │                                                            │
  │  4. challenge.ack                                          │
  │    { from: "A", proof: sign(skA, "rB|A|B") }              │
  ├──────────────────────────────►─────────────────────────────►│
  │                                                            │
  │  5. Node B 验证 proof：                                     │
  │    verify(pkA, "rB|A|B", proof) == true                    │
  │                                                            │
  │  ══════════ 握手完成，双向已认证 ══════════                    │
  │                                                            │
  │  6. 后续消息附带签名                                         │
  │    { from: "A", content: "...",                            │
  │      sig: sign(skA, sha256(content|ts|A)) }               │
  ├──────────────────────────────►─────────────────────────────►│
  │                                                            │
  │  7. Node B 验证签名                                         │
  │    verify(pkA, sha256(content|ts|A), sig) == true          │
```

**Subject 规范：**

```
openclaw.auth.<targetNodeId>.challenge.request
openclaw.auth.<targetNodeId>.challenge.response
openclaw.auth.<targetNodeId>.challenge.ack
```

**挑战请求载荷：**

```json
{
  "from": "node-alpha",
  "publicKey": "base64url-ed25519-public-key",
  "nonce": "random-32-bytes-base64url",
  "ts": 1741680000000
}
```

**挑战响应载荷：**

```json
{
  "from": "node-beta",
  "publicKey": "base64url-ed25519-public-key",
  "nonce": "random-32-bytes-base64url",
  "proof": "base64url-ed25519-signature-of-nonceA|B|A"
}
```

**确认载荷：**

```json
{
  "from": "node-alpha",
  "proof": "base64url-ed25519-signature-of-nonceB|A|B"
}
```

### 消息签名

握手完成后，所有点对点消息必须携带 `sig` 字段：

```
sig = Ed25519.sign(privateKey, SHA-256(content + "|" + ts + "|" + from))
```

接收方验证：

```
Ed25519.verify(senderPublicKey, SHA-256(content + "|" + ts + "|" + from), sig)
```

同时检查 `ts` 与本地时间的偏差不超过阈值（默认 60 秒），防止重放攻击。

### 认证状态机

每个 peer 连接在 Sidecar 中维护一个认证状态：

```
┌───────────┐    收到 announce    ┌──────────┐   挑战成功   ┌──────────────┐
│ unknown   ├───────────────────►│ seen     ├────────────►│ authenticated│
└───────────┘                    └────┬─────┘             └──────┬───────┘
                                      │ 挑战失败                  │ TTL 过期
                                      ▼                          ▼
                                 ┌──────────┐             ┌───────────┐
                                 │ rejected │             │ expired   │
                                 └──────────┘             └───────────┘
```

- **unknown**: 从未见过
- **seen**: 已通过 announce 发现，但未完成握手
- **authenticated**: 握手成功，可收发签名消息
- **rejected**: 握手失败或公钥不在信任列表中
- **expired**: 超过 TTL 未收到心跳

## 组件详情

### 1. Sidecar 服务（独立进程）

Sidecar 是一个**独立于任何 Agent 产品**的长驻进程。

**核心职责：**

- 维持 NATS 持久连接
- 订阅 `openclaw.agent.<nodeId>.inbox` 接收入站消息
- 订阅 `openclaw.discovery.announce` 参与节点发现
- 管理 Ed25519 密钥对和认证握手
- 调用本地 Agent 的公开 API 投递消息
- 定期发布心跳
- 维护节点目录和认证状态

**适配层接口（不同 Agent 产品实现不同）：**

```typescript
// Sidecar 的 Agent 适配层接口
interface AgentAdapter {
  // 将 NATS 入站消息投递给本地 Agent
  deliverMessage(params: {
    sessionKey: string;
    message: string;
    from: string;
    metadata?: Record<string, unknown>;
  }): Promise<{ success: boolean; reply?: string }>;

  // 获取 Agent 运行状态（可选）
  getStatus?(): Promise<{ healthy: boolean }>;
}
```

**OpenClaw 适配层实现：**

```typescript
// 通过 WebSocket 调用 OpenClaw gateway
class OpenClawAdapter implements AgentAdapter {
  async deliverMessage(params) {
    return this.gatewayClient.request("chat.send", {
      sessionKey: params.sessionKey,
      message: params.message,
      idempotencyKey: randomUUID(),
    });
  }
}
```

**其他 Agent 产品只需实现 `AgentAdapter` 接口即可接入。**

**配置（环境变量或配置文件）：**

```bash
# NATS 连接
NATS_SERVERS=nats://localhost:4222
NATS_TOKEN=可选的认证令牌
NATS_CREDS_FILE=/path/to/creds

# 节点身份
NODE_ID=node-alpha
NODE_CAPABILITIES=chat,tools,voice

# Agent 适配（以 OpenClaw 为例）
AGENT_ADAPTER=openclaw
GATEWAY_URL=ws://127.0.0.1:18789
GATEWAY_TOKEN=your-gateway-token

# 节点发现
HEARTBEAT_INTERVAL_MS=15000
ANNOUNCE_TTL_MS=30000

# 身份认证
TRUST_MODE=tofu
IDENTITY_KEY_PATH=~/.nats-bridge/identity.key

# 可选：转发 Agent 事件到 NATS
BRIDGE_EVENTS=agent_end,message_sent
```

**启动流程：**

```
1. 加载或生成 Ed25519 密钥对
2. 连接 NATS
3. 连接本地 Agent 网关（通过 AgentAdapter）
4. 订阅 openclaw.agent.<nodeId>.inbox
5. 订阅 openclaw.discovery.announce
6. 订阅 openclaw.auth.<nodeId>.challenge.*
7. 发布初始注册信息（含公钥）
8. 启动心跳定时器
9. 开始处理入站消息
```

### 2. Skill: nats_publish

Agent 调用此 Skill 向 NATS 发布消息。

**调用参数：**

```json
{
  "targetNode": "node-beta",
  "message": "请汇总最新报告",
  "sessionKey": "nats:node-alpha:node-beta",
  "metadata": { "priority": "high" }
}
```

当提供 `targetNode` 时，Skill 从节点目录解析 inbox subject（或回退到
`openclaw.agent.<targetNode>.inbox`）。

也可直接指定 subject：

```json
{
  "subject": "openclaw.broadcast.task-queue",
  "message": "新任务已就绪"
}
```

**request-reply 模式（同步等待回复）：**

```json
{
  "targetNode": "node-beta",
  "message": "当前状态如何？",
  "waitForReply": true,
  "timeoutMs": 30000
}
```

**实现要点：**

- 打开短时 NATS 连接，发布消息，断开。无持久状态。
- 消息自动附带 `sig` 签名（如果启用了身份认证）。
- 签名使用 Sidecar 管理的私钥，Skill 通过本地 IPC 或文件获取。

### 3. Skill: nats_peers

只读 Skill，返回当前在线节点列表：

```json
[
  {
    "nodeId": "node-beta",
    "agentProduct": "openclaw",
    "version": "2026.3.9",
    "capabilities": ["chat", "tools"],
    "inbox": "openclaw.agent.node-beta.inbox",
    "authStatus": "authenticated",
    "lastSeen": "2026-03-11T10:00:15Z",
    "metadata": { "hostname": "server-2", "channels": ["slack"] }
  },
  {
    "nodeId": "node-gamma",
    "agentProduct": "langchain",
    "version": "0.3.1",
    "capabilities": ["chat", "rag"],
    "inbox": "openclaw.agent.node-gamma.inbox",
    "authStatus": "seen",
    "lastSeen": "2026-03-11T10:00:12Z"
  }
]
```

Agent 可根据节点列表、能力和认证状态决定消息路由。

## 验证网关通信

在部署 Sidecar 之前，先验证本地 Agent 网关是否正常响应。以下两种方式均可独立于
NATS 使用，也是 Sidecar 适配层调用的底层接口。

### CLI 快速验证

通过 `openclaw agent` 命令直接与网关 Agent 交互：

```bash
# 启动网关（开发模式使用 --dev）
openclaw gateway run          # 生产模式，默认端口 18789
openclaw --dev gateway run    # 开发模式，默认端口 19001

# 发送单条消息给指定 Agent
OPENCLAW_GATEWAY_TOKEN=your-gateway-token \
  openclaw agent --agent <agentId> --message "你好"

# 开发模式示例
OPENCLAW_GATEWAY_TOKEN=your-gateway-token \
  openclaw --dev agent --agent dev --message "你好"
```

`--agent` 的值对应 `openclaw.json` 中 `agents.list[].id`。用
`openclaw agents list` 查看已配置的 Agent。

### WebSocket 调用验证

Sidecar 适配层通过 WebSocket 与网关通信。以下是完整的握手 + `chat.send` 流程
（Node.js 示例，需安装 `ws` 包）：

```javascript
const WebSocket = require("ws");
const ws = new WebSocket("ws://127.0.0.1:18789"); // 开发模式用 19001
let reqId = 0;

ws.on("message", (data) => {
  const msg = JSON.parse(data.toString());

  // 1. 响应握手挑战
  if (msg.event === "connect.challenge") {
    ws.send(JSON.stringify({
      type: "req",
      id: String(++reqId),
      method: "connect",
      params: {
        minProtocol: 3,
        maxProtocol: 3,
        client: {
          id: "gateway-client",     // 有效值见 GATEWAY_CLIENT_IDS
          version: "0.0.1",
          platform: "macos",
          mode: "backend",          // 有效值见 GATEWAY_CLIENT_MODES
        },
        role: "operator",
        scopes: ["operator.read", "operator.write"],
        caps: [], commands: [], permissions: {},
        auth: { token: "your-gateway-token" },
        locale: "zh-CN",
        userAgent: "my-sidecar/0.0.1",
      },
    }));
  }

  // 2. 连接成功后发送消息
  if (msg.type === "res" && msg.ok && msg.payload?.type === "hello-ok") {
    ws.send(JSON.stringify({
      type: "req",
      id: String(++reqId),
      method: "chat.send",
      params: {
        message: "你好",
        sessionKey: "nats:node-alpha:node-beta",
        idempotencyKey: crypto.randomUUID(),
      },
    }));
  }

  // 3. chat.send 立即返回 { runId, status: "started" }
  //    Agent 回复通过 "agent" 事件流式返回（stream: "text"）
  //    运行结束后收到 "chat" 事件（state: "final"）

  // 4. 获取完整回复：调用 chat.history
  if (msg.event === "chat" && msg.payload?.state === "final") {
    ws.send(JSON.stringify({
      type: "req",
      id: String(++reqId),
      method: "chat.history",
      params: {
        sessionKey: "nats:node-alpha:node-beta",
        limit: 5,
      },
    }));
  }

  // 5. history 响应包含 messages 数组
  if (msg.type === "res" && msg.ok && msg.payload?.messages) {
    console.log(msg.payload.messages);
    ws.close();
  }
});
```

**关键说明：**

- `client.id` 必须为协议定义的有效值（如 `gateway-client`、`cli`、`webchat` 等）
- `client.mode` 必须为有效值（如 `backend`、`cli`、`webchat` 等）
- `chat.send` 是非阻塞的：立即返回 `{ runId, status: "started" }`，回复通过事件流送达
- 获取完整回复文本需调用 `chat.history`
- 协议详情参见 [Gateway Protocol](/gateway/protocol)

## 部署

### 前置条件

- 运行中的 NATS 服务器（本地或远程），快速启动：

```bash
docker run -d --name nats -p 4222:4222 nats:latest
```

- 本地运行的 Agent 网关（以 OpenClaw 为例：`openclaw gateway run`）

### 运行 Sidecar

```bash
# 安装
npm install -g @openclaw/nats-sidecar

# 或从源码运行
node sidecar.mjs \
  --nats-servers nats://localhost:4222 \
  --node-id node-alpha \
  --agent-adapter openclaw \
  --gateway-url ws://127.0.0.1:18789 \
  --gateway-token "$GATEWAY_TOKEN"
```

### 多节点异构部署示例

```
┌──────────────────┐     ┌──────────┐     ┌──────────────────┐
│ Machine A        │     │          │     │ Machine B        │
│                  │     │   NATS   │     │                  │
│ OpenClaw Gateway │     │  Server  │     │ LangChain Agent  │
│ Sidecar A ───────├─────┤          ├─────┤── Sidecar B      │
│ (node-alpha)     │     │          │     │ (node-beta)      │
│ adapter=openclaw │     │          │     │ adapter=langchain│
└──────────────────┘     └────┬─────┘     └──────────────────┘
                              │
                    ┌─────────┴────────┐
                    │ Machine C        │
                    │                  │
                    │ 自研 Agent 服务   │
                    │ Sidecar C ────── │
                    │ (node-gamma)     │
                    │ adapter=custom   │
                    └──────────────────┘
```

每台机器运行自己的 Agent + Sidecar。NATS 服务器可在任一台机器或第三方主机上。
Sidecar 通过 announce 协议自动发现彼此，通过握手互相认证。

## 安全考量

### 传输层

- **NATS 认证**：生产环境使用 token 或 NKey/凭证文件；不要暴露未认证的 NATS
  服务器到公网。
- **TLS**：跨网络部署时在 NATS 服务器启用 TLS，防止中间人攻击。

### 应用层

- **节点身份认证**：通过 Ed25519 挑战-响应握手验证节点身份（见上文）。
- **消息签名**：所有点对点消息携带 Ed25519 签名，防伪造和篡改。
- **重放保护**：验证消息 `ts` 与本地时间偏差不超过阈值（默认 60 秒）。
- **密钥轮换**：支持定期轮换密钥；新公钥通过 announce 广播，旧密钥在宽限期
  （默认 24 小时）内仍被接受。

### Agent 网关层

- **网关认证**：Sidecar 使用与其他客户端相同的 device token 或 shared token
  机制连接本地 Agent 网关。
- **消息校验**：Sidecar 在转发到网关前验证 JSON 载荷格式，拒绝超大或畸形消息。

### 隔离策略

- **Subject 权限**：使用 NATS account/user 权限限制节点的发布/订阅范围。
- **不传输密钥**：永远不在 NATS 消息中包含 API key、token 或凭证。

## 方案对比

| 方案 | 耦合度 | 可移植性 | 复杂度 | 身份认证 |
|------|--------|----------|--------|----------|
| Agent 内部插件 | 高 | 仅限特定产品 | 中 | 依赖插件 SDK |
| 网关直连 WebSocket | 中 | 仅限特定产品 | 高 | 依赖网关认证 |
| HTTP Webhook 中继 | 低 | 任意 Agent | 中 | 需自行实现 |
| **NATS Sidecar + Skill** | **低** | **任意 Agent** | **低** | **Ed25519 内置** |

选择 Sidecar + Skill 方案的原因：最小耦合、最大可移植性。Sidecar 只使用两个
公开协议（NATS + Agent 公开 API），任何能连接 NATS 的 Agent 系统都可以接入，
无需引入对方的内部依赖。

## 相关文档

- [Gateway Protocol](/gateway/protocol) -- WebSocket 帧格式和 `chat.send` 参数
- [Networking and Discovery](/gateway/network-model) -- 网关的本地网络发现机制
- [Bonjour](/gateway/bonjour) -- 基于 mDNS 的局域网发现（与 NATS 发现互补）

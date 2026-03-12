---
summary: "ClawSynapse 集成与适配：本地 API、Adapter 接口与 OpenClaw 接入"
title: "ClawSynapse Integration"
---

# ClawSynapse Integration

最后更新：2026-03-12

## 本地 API

`clawsynapsed` 暴露本地 API，供 Agent 或 Skill 调用。

这里展示的是集成侧调用方式。涉及 subject 命名、Envelope 字段和消息语义时，以 `docs/clawsynapse-protocol.md` 为准。

接口：

```http
POST /v1/publish
GET /v1/peers
POST /v1/request
```

`POST /v1/publish` 示例：

```json
{
  "targetNode": "node-beta",
  "message": "请汇总最新报告",
  "sessionKey": "nats:node-alpha:node-beta",
  "metadata": { "priority": "high" }
}
```

`POST /v1/request` 示例：

```json
{
  "targetNode": "node-beta",
  "message": "当前状态如何？",
  "waitForReply": true,
  "timeoutMs": 30000
}
```

直接指定 subject 的发布示例：

```json
{
  "subject": "clawsynapse.broadcast.task-queue",
  "message": "新任务已就绪"
}
```

其中 subject 命名只是示例，实际命名规范应遵循 `docs/clawsynapse-protocol.md`。

`GET /v1/peers` 响应示例：

```json
[
  {
    "nodeId": "node-beta",
    "agentProduct": "openclaw",
    "version": "2026.3.9",
    "capabilities": ["chat", "tools"],
    "inbox": "clawsynapse.agent.node-beta.inbox",
    "authStatus": "authenticated",
    "lastSeen": "2026-03-11T10:00:15Z",
    "metadata": { "hostname": "server-2", "channels": ["slack"] }
  }
]
```

返回中的 `inbox`、`authStatus` 等字段可视为集成层投影；其底层命名与状态值应与 `docs/clawsynapse-protocol.md` 保持一致。

本地 API 负责接收业务请求，再由守护进程统一完成路由、握手、签名、发布和回复等待。

## Agent Adapter

不同 Agent 产品通过实现统一接口接入。

```go
type AgentAdapter interface {
    DeliverMessage(ctx context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error)
    GetStatus(ctx context.Context) (*AgentStatus, error)
}

type DeliverMessageRequest struct {
    SessionKey string
    Message    string
    From       string
    Metadata   map[string]any
}

type DeliverMessageResult struct {
    Success  bool
    Accepted bool
    RunID    string
    Reply    string
    Error    string
}

type AgentStatus struct {
    Healthy bool
}
```

首批实现：

- `OpenClawAdapter`
- 扩展 `CustomHTTPAdapter`、`WorkflowAdapter` 等

## OpenClaw 接入

第一阶段实现 `OpenClawAdapter`：

- `clawsynapsed` 与本地 OpenClaw Gateway 建立 WebSocket 长连接
- 完成 gateway 握手后调用 `chat.send`
- 将 `sessionKey` 映射为跨节点会话标识
- 获取最终回复时，监听运行完成事件后调用 `chat.history`

### 网关通信验证

CLI 验证：

```bash
openclaw gateway run
OPENCLAW_GATEWAY_TOKEN=your-gateway-token \
  openclaw agent --agent <agentId> --message "你好"
```

WebSocket 调用流程：

1. 连接本地 Gateway
2. 响应 `connect.challenge`
3. 发送 `connect`
4. 发送 `chat.send`
5. 在运行结束后调用 `chat.history` 获取最终回复

简化示例：

```javascript
const ws = new WebSocket("ws://127.0.0.1:18789");

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);

  if (msg.event === "connect.challenge") {
    ws.send(JSON.stringify({
      type: "req",
      id: "1",
      method: "connect",
      params: {
        minProtocol: 3,
        maxProtocol: 3,
        client: { id: "gateway-client", version: "0.0.1", platform: "macos", mode: "backend" },
        role: "operator",
        scopes: ["operator.read", "operator.write"],
        auth: { token: "your-gateway-token" }
      }
    }));
  }

  if (msg.type === "res" && msg.ok && msg.payload?.type === "hello-ok") {
    ws.send(JSON.stringify({
      type: "req",
      id: "2",
      method: "chat.send",
      params: {
        message: "你好",
        sessionKey: "nats:node-alpha:node-beta",
        idempotencyKey: crypto.randomUUID()
      }
    }));
  }
};
```

## 扩展方向

- 自研 Agent 通过 HTTP 或 WebSocket 提供本地投递接口
- 其他 Agent 产品通过适配层实现统一接入
- 后续可以补充本地 webhook 或事件流接口，支持更松耦合集成

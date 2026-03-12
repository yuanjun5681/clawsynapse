---
summary: "ClawSynapse 集成与适配：本地 API、Adapter 接口与 OpenClaw 接入"
title: "ClawSynapse Integration"
---

# ClawSynapse Integration

最后更新：2026-03-12

## 本地 API

`clawsynapsed` 暴露本地 API，供 Agent 或 Skill 调用。

接口：

```http
POST /v1/publish
GET /v1/peers
POST /v1/request
```

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
- 扩展 `CustomHTTPAdapter`、`LangChainAdapter` 等

## OpenClaw 接入

第一阶段实现 `OpenClawAdapter`：

- `clawsynapsed` 与本地 OpenClaw Gateway 建立 WebSocket 长连接
- 完成 gateway 握手后调用 `chat.send`
- 将 `sessionKey` 映射为跨节点会话标识
- 获取最终回复时，监听运行完成事件后调用 `chat.history`

## 扩展方向

- 自研 Agent 通过 HTTP 或 WebSocket 提供本地投递接口
- 其他 Agent 产品通过适配层实现统一接入
- 后续可以补充本地 webhook 或事件流接口，支持更松耦合集成

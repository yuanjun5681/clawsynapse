---
summary: "ClawSynapse 集成与适配：Adapter 接口、OpenClaw 接入与 Webhook 接入"
title: "ClawSynapse Integration"
---

# ClawSynapse Integration

最后更新：2026-10-20

本文档介绍如何通过 Agent Adapter 适配层将不同 Agent 产品接入 ClawSynapse。

本地 HTTP API 的完整接口说明见 [docs/api.md](api.md)。CLI 用法见 [docs/cli.md](cli.md)。消息协议与 subject 命名规范见 [docs/protocol.md](protocol.md)。

## Agent Adapter

当守护进程收到发往本节点的消息时，通过 `AgentAdapter` 接口投递给本地 Agent。不同 Agent 产品实现该接口即可接入。

```go
type AgentAdapter interface {
    DeliverMessage(ctx context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error)
    GetStatus(ctx context.Context) (*AgentStatus, error)
}
```

### DeliverMessageRequest

| 字段 | 类型 | 说明 |
|------|------|------|
| `MessageID` | string | 当前消息 ID |
| `RequestID` / `RunID` / `Seq` | string / string / uint64 | 执行事件关联字段 |
| `Type` | string | 消息类型（如 `chat.message`, `task.assign`） |
| `AgentID` | string | 接收节点本地授权列表内的目标 Agent；不支持时明确拒绝 |
| `SessionKey` | string | LLM 调用已按发送方和 Agent 隔离的会话键；Webhook 保留原键 |
| `Message` | string | 文本快捷输入；结构化任务以 Payload 为准 |
| `Payload` | protocol.MessagePayload | text、instruction/input 或执行反馈 |
| `Emit` | func(OutputEvent) error | 同步背压的答案增量回调；错误须停止执行 |
| `From` | string | 发送方节点 ID |
| `Metadata` | map[string]any | 附加元数据 |

### DeliverMessageResult

| 字段 | 类型 | 说明 |
|------|------|------|
| `Success` | bool | 投递是否成功 |
| `Accepted` | bool | Agent 是否接受了消息 |
| `Completed` | bool | 已确认同步终态；只有 Accepted 不表示完成 |
| `RunID` | string | Agent 运行 ID（如适用） |
| `Reply` | string | Agent 的回复内容 |
| `Error` | string | 错误描述（失败时） |

### AgentStatus

| 字段 | 类型 | 说明 |
|------|------|------|
| `Healthy` | bool | Agent 是否健康 |
| `Degraded` / `Detail` | bool / string | 探针限制与非敏感诊断，不把 CLI 存在等同于模型可用 |

### 系统消息投递

除了由 `DELIVERABLE_PREFIXES` 控制的用户消息外，部分系统消息会**直接投递**给 Adapter，不受前缀过滤限制：

| 消息类型 | 触发时机 | 说明 |
|----------|----------|------|
| `transfer.received` | 文件接收完成并落盘后 | 通知本地 Agent 有新文件到达 |

#### `transfer.received` 消息

当远端节点向本节点发送文件，文件下载并保存到本地后，守护进程自动通过 Adapter 投递一条 `transfer.received` 消息。

`Payload.Input` 为结构化 JSON，包含传输固有信息：

| 键 | 类型 | 说明 |
|----|------|------|
| `transferId` | string | 传输 ID |
| `fileName` | string | 文件名 |
| `fileSize` | int64 | 文件大小（字节） |
| `localPath` | string | 本地文件路径 |
| `mimeType` | string | 文件 MIME 类型 |

聊天参与者是节点，`targetNode` 是唯一路由身份；DSH 集成不应发送 `agentId`。每个节点只有一个 DSH 产品，节点收到消息后交给本机 DSH，DSH 内部的 Agent/preset 选择不对外暴露。`agentId` 仅保留为通用 CLI/Webhook 适配器的可选内部提示；`agentAllowedIds` 只用于这些适配器的本地校验，不是聊天目录。`run.*`、`*.response`、`*.error` 反馈携带原始执行方身份；`chat.context` 与 `transfer.received` 是观察/系统通知，不触发模型执行。

`Metadata` 字段为发送方附带的业务扩展元数据（如 `taskId`、`todoId`），与 `POST /v1/publish` 中 `metadata` 的语义一致。

CLI Adapter 将请求编码为 JSON prompt，不再拼接未转义的 `key=value` header。Webhook 同样接收结构化 payload，例如：

```json
{
  "nodeId": "n1-local",
  "messageId": "transfer.received:tf_abc123",
  "type": "transfer.received",
  "from": "n1-sender",
  "message": "",
  "payload": {
    "input": {
      "transferId": "tf_abc123",
      "fileName": "report.pdf",
      "fileSize": 24576,
      "localPath": "/data/transfers/tf_abc123-report.pdf",
      "mimeType": "application/pdf"
    }
  },
  "metadata": {"taskId": "task-001"}
}
```

### 已实现的适配器

| 适配器 | `AGENT_ADAPTER` 值 | 说明 |
|--------|---------------------|------|
| `DefaultAdapter` | `default` | 内置默认，echo 回显，用于调试和测试 |
| `OpenClawAdapter` | `openclaw` | 调用本地 OpenClaw CLI 投递消息 |
| `OpenCodeAdapter` | `opencode` | 通过原生 JSONL 事件增量投递答案 |
| `CodexAdapter` | `codex` | stdin prompt、原生 JSONL、明确 turn.completed 终态 |
| `WebhookAdapter` | `webhook` | 通过 HTTP POST 将消息 JSON 转发到外部 URL |

通过环境变量 `AGENT_ADAPTER` 或命令行 `--agent-adapter` 选择适配器。
消息投递超时默认是 10 分钟，可通过 `AGENT_ADAPTER_TIMEOUT`、`--agent-adapter-timeout` 或 YAML 中的 `agentAdapterTimeout` 覆盖。

### CLI 可靠性契约

- Codex/OpenCode 增量读取原生事件；只发布答案，不转发 thinking、reasoning 或工具事件。Codex 必须出现 `turn.completed`；OpenCode 要求成功 `step_finish`，`tool-calls` 不是最终完成。
- OpenClaw 保留 Gateway-backed `agent --json` 调用，以明确成功 status/result 判定；一次性 JSON 响应不宣称原生流式。
- 未知输出、登录提示、仅启动事件、非零退出或中断不报完成；会话失效不自动新建重跑。
- 单原生事件或一次性 JSON 响应最多 256 KiB，累计输出最多 4 MiB，stderr 最多 8 KiB；prompt 最多 32 KiB，argv prompt 最多 16 KiB。超限明确失败，不静默截断成功。
- 首个答案立即发布，后续小片段按最多 4 KiB 合并，终止时刷新。回调背压同步传到进程，回调错误会取消执行。
- Unix 使用独立进程组，Windows 使用 Job Object 管理进程树；超时、取消与 WaitDelay 有界。Windows 已交叉编译，实际运行仍需目标 OS 验收。
- 产品会话读写失败显式报错。仅受当前原生事件契约支持的 CLI 可用；help/auth/health 探针只验证非交互能力，不替代真实模型任务验收。
- 有 Emit 时结果不再重复返回完整 Reply；失败使用 ExecutionError 的 code、stage 和 indeterminate，不泄露原生 stdout/stderr。
- DSH 集成只需节点显示名与 `labels`；不要为聊天配置 Agent 名称或 Agent 目录。通用 CLI 适配器仍可使用 `--agent-allowed-ids` 约束本地目标。


---

## OpenClaw 接入

`OpenClawAdapter` 通过调用本地 `openclaw` CLI 将消息投递给 OpenClaw Agent。

### 消息格式

投递时把来源、消息关联、会话、payload 和 metadata 编码为 JSON prompt，字段值不再解释为可拼接的 header。JSON 结构不能消除提示词注入；授权和工具限制必须由本地策略落实。

只有配置在 `agentAllowedIds`／`AGENT_ALLOWED_IDS`／`--agent-allowed-ids` 中的命名 AgentID 才会转换为 `--agent`。保留 ID `default` 使用产品原生默认 Agent，不传 `--agent default`；未配置列表时只允许该默认目标。该列表只影响本地 CLI 路由，不进入节点发现或聊天参与者目录。

### 会话映射

- 正常消息的产品会话由发送方、目标 Agent 与 sessionKey 的作用域摘要确定。
- 缺少会话键时使用独立消息关联；已失效会话明确失败，由调用方显式创建新会话。

### 网关通信验证

CLI 验证：

```bash
openclaw gateway run
OPENCLAW_GATEWAY_TOKEN=your-gateway-token \
  openclaw agent --message "你好"
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
        sessionKey: "nats:n1-localnodeid:n1-11223344556677889900aabbccddeeff",
        idempotencyKey: crypto.randomUUID()
      }
    }));
  }
};
```

---

## Webhook 接入

`WebhookAdapter` 在收到订阅消息后，将消息以 JSON 格式通过 HTTP POST 转发到指定的 webhook URL，适用于与外部系统或自研 Agent 的松耦合集成。

### 配置

环境变量：

```bash
AGENT_ADAPTER=webhook
WEBHOOK_URL=https://example.com/hooks/clawsynapse
WEBHOOK_AUTH_TOKEN='<random-secret>'
```

命令行：

```bash
clawsynapsed --agent-adapter webhook --webhook-url https://example.com/hooks/clawsynapse
```

YAML 配置文件（`~/.clawsynapse/config.yaml`）：

```yaml
agentAdapter: webhook
agentAdapterTimeout: 10m
webhookUrl: https://example.com/hooks/clawsynapse
```

`WEBHOOK_AUTH_TOKEN` 是敏感值，可从 `clawsynapsed` 进程的 OS 环境或项目根目录 `.env` 读取，不支持 YAML 或命令行参数，也不会出现在配置 API 和 `--check-config` 输出中。未设置或为空时不发送认证头，以兼容现有 webhook；生产环境建议始终设置随机 token。

### Webhook Payload

每次消息投递时，`WebhookAdapter` 向配置的 URL 发送 `POST` 请求，`Content-Type: application/json`，body 格式：

```json
{
  "nodeId": "n1-2f4c6e8a0b1d3f557799aabbccddeeff",
  "messageId": "m1-01hzy8example",
  "type": "chat.message",
  "from": "n1-11223344556677889900aabbccddeeff",
  "sessionKey": "nats:n1-11223344556677889900aabbccddeeff:n1-2f4c6e8a0b1d3f557799aabbccddeeff",
  "message": "请汇总最新报告",
  "payload": {"text": "请汇总最新报告"},
  "metadata": { "priority": "high" }
}
```

字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `nodeId` | string | 本地节点 ID，由本地 DID 自动派生 |
| `messageId` | string | ClawSynapse 原始消息 ID，可用于幂等去重、审计和关联回复 |
| `requestId` / `runId` / `seq` | string / string / uint64 | run 事件的原请求、节点执行 ID 和序号 |
| `type` | string | 消息类型（如 `chat.message`, `task.assign`） |
| `from` | string | 发送方节点 ID |
| `sessionKey` | string | 会话标识，可能为空 |
| `message` | string | 消息正文 |
| `payload` | object | 结构化请求或执行事件；不能只消费 message |
| `metadata` | object | 附加元数据，可能为空 |

设置 `WEBHOOK_AUTH_TOKEN` 后，POST 投递和 GET 健康检查都会携带：

```http
Authorization: Bearer <token>
```

token 不会写入请求 URL、日志或错误正文。

### 响应约定

- **2xx**：按返回内容区分完成与异步接纳；空响应只表示 Accepted。
- 纯文本或 JSON 字符串作为同步答案；结构化对象读取 reply/message，以及 runId、sessionId、accepted、success、completed、error。
- `completed: false` 不发布答案完成；`completed: true` 只表示 Webhook 契约确认的执行终态，不是设备物理凭据。
- 非流式小响应仍限制 4 KiB，超限或读取失败会报错，不以截断正文判成功；完整长答案使用下述 NDJSON。
- **非 2xx**：视为投递失败，仅返回状态码，不把响应正文泄露到错误中。

推荐 Node webhook 直接返回结构化 JSON，例如：

```json
{
  "reply": "line1\nline2",
  "accepted": true,
  "completed": true
}
```

只要业务端正常使用 JSON 解析，字符串中的 `\n` 会自动还原为真实换行；不要再对已经解析好的字符串做额外转义处理。

### 完整输出流

POST 请求发送 `Accept: application/x-ndjson, application/json`。支持流式响应的集成返回 `Content-Type: application/x-ndjson`，每行一个 JSON frame：

```jsonl
{"type":"output","text":"第一段完整答案"}
{"type":"output","text":"后续答案，可以包含换行和 Unicode"}
{"type":"completed"}
```

失败终态为 `{"type":"failed","error":"说明","indeterminate":true}`；确定失败可省略 indeterminate 或设为 false。每个 output.text 最多 64 KiB UTF-8，编码后的单行必须小于 128 KiB；没有总答案截断。Daemon 每收到一个 output 就以同步背压写入持久 run.output.delta，再继续读取。结果可在模型完成后分段输出，不要求是真正 token streaming；最后只发送一个终态，不能在 completed 后再输出。

EOF、超限、无效 JSON/未知 frame、或建立连接后的传输中断均表示结果 indeterminate；已收到的 partial output 保留，不能当作 completed 或安全自动重跑。只有 dial 根本未建立才可按相同 messageId 重试。集成必须持久去重 queued/admitted/completed 命令：相同 messageId 不得重复启动模型或外部动作；已完成可返回持久结果，无法确认的 admitted 状态必须显式 failed/indeterminate，不能伪造成功。

普通命令包含 requestId=messageId、daemon 持久 runId 和当前 seq，反馈保持其原有关联字段。run.*、chat.context 反馈与 transfer.received 是非唤醒数据，不应按用户命令路由；chat.context 只持久投递给 Webhook，由集成保存授权历史，不产生 run 反馈；transfer.received 目前是文件落盘后的本地回调，不是持久消息收件箱中的执行命令。外部副作用不提供 exactly-once 保证。


### 健康检查

`GetStatus` 对 webhook URL 发送 `GET` 请求；配置 token 时使用与 POST 相同的 Bearer 认证：

- 状态码 < 500，且不是 401/403 → healthy
- 状态码为 401/403、>= 500 或连接失败 → unhealthy

### 使用场景

- 自研 Agent 暴露 HTTP 接口接收消息
- 转发消息到 Slack、飞书、企业微信等即时通讯平台
- 对接工作流引擎（n8n、Zapier 等）
- 消息归档、审计日志

---

## 扩展方向

- 其他 Agent 产品通过适配层实现统一接入
- 后续可补充事件流接口（SSE/WebSocket），支持双向通信

---
summary: "ClawSynapse 集成与适配：Adapter 接口、OpenClaw 接入与 Webhook 接入"
title: "ClawSynapse Integration"
---

# ClawSynapse Integration

最后更新：2026-11-02

本文档介绍如何通过 Agent Adapter 适配层将不同 Agent 产品接入 ClawSynapse。

本地 HTTP API 的完整接口说明见 [docs/api.md](api.md)。CLI 用法见 [docs/cli.md](cli.md)。消息协议与 subject 命名规范见 [docs/protocol.md](protocol.md)。

## Agent Skills

- [clawsynapse](../skills/clawsynapse/SKILL.md)：Bot 通信、身份与信任操作，保留原有消息回复约定。
- [clawsynapse-context](../skills/clawsynapse-context/SKILL.md)：不依赖 ClawSynapse CLI 的通用上下文消费指引，覆盖按需读取、版本、来源、分页、缺失与权限拒绝。

两份 skill 可独立安装，不要求位于同一目录；通信 skill 仅在 Harness 已提供上下文 skill 时按名称引用它。上下文 skill 仅使用实际提供的授权读取工具、内联内容或明确可访问的文件，不新增读取工具或后端能力；当前 CLI 尚无共享上下文读取命令。它不定义存储组织方式，也不执行上下文写入、消息发送或权限变更。

一键安装脚本在所有安装模式中默认安装这两份 Skill 到 `~/.agents/skills/<name>/SKILL.md`；当前 [OpenCode](https://opencode.ai/docs/skills/) 和 [OpenClaw](https://docs.openclaw.ai/tools/skills) 均支持该共享用户级目录。安装目录属于运行安装脚本的用户，不使用 sudo 写入；不要以 root 身份运行脚本来代替普通用户安装。

可用 `--skills-dir DIR` 指定其他 Harness 的发现目录，或用 `--no-skills` 跳过安装。远程安装按对应版本标签获取内容，`latest` 采用刚安装的二进制版本；源码目录中的本地 `dist/` 安装使用本地 Skill。下载两份内容成功后才开始更新目标文件，失败会阻止安装器报告成功。重装覆盖两个 `SKILL.md`，但不删除同目录中的其他文件。所有卸载模式均删除这两个 Skill 目录及其中的自定义内容，不影响其他 Skill；自定义安装目录需在卸载时再次传入 `--skills-dir DIR`，`--no-skills` 不跳过卸载。请另存自定义内容。已有会话需按 Harness 的发现机制重新加载。

## Agent Adapter

daemon 为每个本地 Bot 构造独立通信 Runtime；只有 `role=agent` 装配 Harness 绑定和 `AgentAdapter`，收到任务后只投递给它自己的执行入口。`role=console` 持久收件，不构造空 Adapter、执行队列、探针或原生 Session。Bot 是通信联系人/身份，Agent Harness 是本地执行环境；不同 Agent 产品实现该接口即可接入，不能把“共用同一产品”当作执行上下文可共享。

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
| `AgentID` | string | 接收 Bot 本地授权列表内的目标 Agent；不支持时明确拒绝 |
| `SessionKey` | string | LLM 调用已按发送方和 Agent 隔离的会话键；Webhook 保留原键 |
| `Message` | string | 文本快捷输入；结构化任务以 Payload 为准 |
| `Payload` | protocol.MessagePayload | text、instruction/input 或执行反馈 |
| `Emit` | func(OutputEvent) error | 同步背压的答案增量回调；错误须停止执行 |
| `From` | string | 发送方 Bot ID |
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

除了由每 Bot `deliverablePrefixes` 控制的用户消息外，部分系统消息会**直接投递**给 Adapter，不受前缀过滤限制：

| 消息类型 | 触发时机 | 说明 |
|----------|----------|------|
| `transfer.received` | 文件接收完成并落盘后 | 通知本地 Agent 有新文件到达 |

#### `transfer.received` 消息

当远端 Bot 向本 Bot 发送文件，文件下载并保存到本地后，守护进程自动通过 Adapter 投递一条 `transfer.received` 消息。

`Payload.Input` 为结构化 JSON，包含传输固有信息：

| 键 | 类型 | 说明 |
|----|------|------|
| `transferId` | string | 传输 ID |
| `fileName` | string | 文件名 |
| `fileSize` | int64 | 文件大小（字节） |
| `localPath` | string | 仅该 Bot 当前绑定 workspace 内的物化副本路径；不是共享 blob 路径或跨 Bot 授权 |
| `mimeType` | string | 文件 MIME 类型 |

聊天参与者是 Bot，本地发送 API 的 `targetBot` 是唯一路由身份。Bot 收到消息后，通过自己的 Adapter 投递给本地执行引擎；引擎内部执行对象不作为聊天联系人暴露。`agentId` 是通用 CLI/Webhook 适配器的可选内部路由提示；`harness.allowedAgentIds` 只用于这些适配器的本地校验，不是聊天目录。`run.*`、`*.response`、`*.error` 反馈携带原始执行方身份；`chat.context` 与 `transfer.received` 是观察/系统通知，不触发模型执行。网络签名封套仍是 wire v2，`from`/`to` 不改名。

`Metadata` 为发送方附带的业务扩展元数据（如 taskId、todoId），与 `POST /v2/bots/{botId}/publish` 中 metadata 的语义一致。每个本地 Bot 的 HTTP、Webhook 和原生 CLI JSON 身份使用 `botId`；不再使用本地 nodeId/targetNode 别名。

CLI Adapter 将请求编码为 JSON prompt，不再拼接未转义的 `key=value` header。Webhook 同样接收结构化 payload，例如：

```json
{
  "botId": "n1-local",
  "messageId": "transfer.received:tf_abc123",
  "type": "transfer.received",
  "from": "n1-sender",
  "message": "",
  "payload": {
    "input": {
      "transferId": "tf_abc123",
      "fileName": "report.pdf",
      "fileSize": 24576,
      "localPath": "/data/bots/n1-local/workspace/binding-a/transfers/report.pdf",
      "mimeType": "application/pdf"
    }
  },
  "metadata": {"taskId": "task-001"}
}
```

### 已实现的适配器

| 适配器 | `harness.adapter` 值 | 说明 |
|--------|-------------------------|------|
| `DefaultAdapter` | `default` | 内置默认，echo 回显，用于调试和测试 |
| `OpenClawAdapter` | `openclaw` | 调用本地 OpenClaw CLI 投递消息 |
| `OpenCodeAdapter` | `opencode` | 通过原生 JSONL 事件增量投递答案 |
| `CodexAdapter` | `codex` | stdin prompt、原生 JSONL、明确 turn.completed 终态 |
| `PiAdapter` | `pi` | `pi -p --mode json`，原生会话恢复与答案增量 |
| `OMPAdapter` | `omp` | `omp -p --mode json`，原生会话恢复与答案增量 |
| `WebhookAdapter` | `webhook` | 通过 HTTP POST 将消息 JSON 转发到外部 URL |

通过无 token 的本机管理 API/CLI 创建时必须显式指定 `role`。Agent 必须显式提供 `harness.adapter`，`harness.timeout` 默认 10m，并可配置适用的 `harness.model`；不从 adapter 推断 Role、不使用 daemon 环境变量/旧 YAML 的全局 adapter。Bot 配置变更必须先停止并以当前 revision CAS 完整更新，保留原 Role，再显式启动。daemon 的 listener/NATS 默认值与资源预算在 schemaVersion 3 的 config.json，Bot 配置在 bots/{botId}/config.json，详见 operations。

Console Bot 可通过无 token 的本机请求 `POST /v2/workspaces/{workspaceId}/binding` 创建/恢复并取得 scoped token；该入口固定创建 `role=console`，工作区绑定不是 Harness Binding。普通 `chat.message` 在签名、认证和信任校验后提交到该 Bot 的 `state/messages.jsonl`，仅完成 inbox 消费，不生成 Run 或答案；反馈同样只入 journal，不进入模型或形成回复循环。执行请求在持久收件后、执行 admission 前确定拒绝：未过期为 `run.failed`、`payload.code="console.execution_unsupported"`、`indeterminate=false`，过期沿用 `execution.expired`；不生成 started、accepted、答案或 completed，不占用 Harness 执行预算。终态与工作结果先持久提交，再由 outbox 发布，重复投递/重启使用原事件与幂等标识。历史已 admitted 执行仍按既有不确定性恢复，不伪装为从未执行。出站聊天仍使用正常 publish/幂等键，来自 Agent 的答案按 cursor 和 requestId/runId/seq 回放。

Console 的 BotInfo 为 `harnessApplicable=false`；Bot health 的 adapter 为 `applicable=false`、`healthy=false` 且 detail 明确 not applicable。Console 配置必须省略整个 `harness`（含 `null`、`{}`）和 `maxConcurrent`/`maxQueued`，没有 timeout 等执行字段；网络健康单独观察，不要求本地 CLI/模型安装。文件接收、授权、配额与持久引用仍运行，本地 `transfer.received` 回调按角色分派，Console 不调用 Harness 或生成执行反馈；Agent 保留文件通知与工作副本契约。

### 真实原生执行绑定

- 每 Bot 的身份、credentials、状态及 workspace 独立。原生 CLI 实际 cwd 为 `bots/{botId}/workspace/{bindingRevision}`；不是仅给 prompt 添加 Bot 名称。
- 产品会话按 Bot ID、bindingRevision、产品，再结合发送方/Agent/会话键隔离，原生产品 session ID 持久映射。Harness 变更由服务器生成新 bindingRevision，旧会话不自动接回。失效会话明确失败，不新建并重跑。
- OpenCode/Codex/pi/omp 的 `harness.model` 传给原生命令 `--model`。OpenCode 使用原生 `run --format json --standalone`；Codex 使用 `exec --json`；pi/omp 使用 `-p --mode json`。OpenClaw 不支持每调用 model override，配置非空 model 会被拒绝。
- OpenClaw/Webhook 需要明确 `harness.instance`，同 adapter 的未归档 Bot 不允许复用 instance。Webhook 发送 botId、bindingRevision、instance 以及对应身份 headers，接收端也必须按绑定隔离状态和权限。instance 标识不是自动创建远端隔离实例的承诺。
- workspace 不等于 OS 沙箱。原生产品账号、工具权限、文件访问和外部副作用仍需部署策略控制；daemon scoped token 不能取代产品凭据隔离。

### CLI 可靠性契约

- Codex/OpenCode 增量读取原生事件，只发布答案，不转发 thinking、reasoning 或工具事件。Codex 要求 turn.completed；OpenCode 接受成功 step_finish（tool-calls 不是终态）。没有 step_finish 的 OpenCode 版本只能通过只读 `session export --standalone --sanitize` 核验：session ID 匹配、整体 succeeded、末条 idle/succeeded，最近 assistant 必须是本次流中精确 messageID、finish=stop 且有 completed 时间。旧回合、仅有答案或进程 exit 0 都不能代替完成；查询失败/缺少权威证据明确 indeterminate，不重新执行任务。
- OpenClaw 保留 Gateway-backed `agent --json` 调用，以明确成功 status/result 判定；一次性 JSON 响应不宣称原生流式。
- pi/omp 只发布 assistant 的 text delta；无 delta 时使用一次终态文本快照，不转发 thinking、工具或用户内容。必须收到成功 assistant `stopReason=stop` 和 `agent_end`，toolUse、length、error、aborted 或仅答案/exit 0 不算完成。健康探针仅核对 CLI help，返回 degraded，未验证认证或模型连接。
- 未知输出、登录提示、仅启动事件、非零退出或中断不报完成；会话失效不自动新建重跑。
- 单原生事件或一次性 JSON 响应最多 256 KiB，累计输出最多 4 MiB，stderr 最多 8 KiB；prompt 最多 32 KiB，argv prompt 最多 16 KiB。超限明确失败，不静默截断成功。
- 首个答案立即发布，后续小片段按最多 4 KiB 合并，终止时刷新。回调背压同步传到进程，回调错误会取消执行。
- Unix 使用独立进程组，Windows 使用 Job Object 管理进程树；超时、取消与 WaitDelay 有界。Windows 已交叉编译，实际运行仍需目标 OS 验收。
- 产品会话读写失败显式报错。仅受当前原生事件契约支持的 CLI 可用；help/auth/health 探针只验证非交互能力，不替代真实模型任务验收。

版本化 workspace topic 使用独立语义输入 `{instruction,publicContext,requiredContext?}`，
不把路由/执行状态 JSON 当用户正文。接收端为支持的 native Harness 加载本地固定策略，
而不将 peer payload 提升为 system 指令。omp/pi 的 topic 正文经 stdin 传递，
避免位置参数的本地命令分派与 argv 正文暴露；字节与必需上下文预算仍独立校验。
原生装入记录需核对持久 user entry、实际输入摘要和会话连续性；普通 peer 集成
仍保留原封套及回复关联契约。完整同步与恢复语义见 [protocol.md](protocol.md#主题公开上下文增量协议)。
- 有 Emit 时结果不再重复返回完整 Reply；失败使用 ExecutionError 的 code、stage 和 indeterminate，并在 `run.failed.payload.message` 中提供可选固定安全摘要，不泄露原生 stdout/stderr、原始错误、凭据或提示词。仅检查结构化错误字段；无效模型为 `invalid_model / validation`，provider.free-limit 为 `provider_quota / provider`，不是会话丢失。已观察到推理、工具或答案后，后续拒绝也保留不确定性；传输与未知运行时错误不伪装成确定未执行。错误不触发自动重跑或会话替换，详见 [messaging.md](messaging.md)。
- DSH 集成只需 Bot 显示名与 labels；不要为聊天配置 Agent 名称或 Agent 目录。通用 CLI 适配器可使用 `harness.allowedAgentIds` 约束本地目标。真实模型任务与第三方 CLI 终态兼容必须单独验收，不能从 health/仅答案输出推断完整 acceptance 通过。

### pi / omp 接入

在 Agent Bot 的 `harness` 中设置 `adapter` 和产品可解析的 `model`，例如：

```json
{"displayName":"pi-worker","role":"agent","harness":{"adapter":"pi","model":"zai-coding-cn/glm-5.3-flash","timeout":"10m"}}
```

```json
{"displayName":"omp-worker","role":"agent","harness":{"adapter":"omp","model":"zai/glm-5.3-flash","timeout":"10m"}}
```

安装对应 CLI，并在 daemon 宿主账号配置产品自己的 provider 凭据；provider 名称取决于本机模型目录，不将上述两个前缀视为可互换别名。`credentialsFile` 是 Webhook 凭据，不用于注入原生 CLI 模型密钥。安装脚本会将检测到的 pi/omp 所在目录及 `~/.bun/bin` 纳入服务 PATH；CLI 在安装服务之后才加入其他目录时，需更新服务 PATH。

两者不支持 `agentId` 命名路由，即使设置 `allowedAgentIds` 也会拒绝。原生 Session 文件位于该 Binding 工作目录的 `.clawsynapse/pi-sessions` 或 `.clawsynapse/omp-sessions`。显式 `--session-dir` 启动新会话；恢复仅允许该目录中与持久 UUID 匹配的文件，pi 使用 `--session FILE`，omp 使用 `--resume FILE`。丢失、损坏、歧义、符号链接或 UUID 不符时明确失败，不自动创建新会话。OMP 文件可有原生 v1 title 元数据前导记录，之后仍须匹配 session 身份头。

2026-11-02 在 macOS 上通过 pi 1.4.0（`zai-coding-cn/glm-5.3-flash`）与 omp 18.15.0（`zai/glm-5.3-flash`）的 Adapter 真实执行验收：流式答案、工具写文件、重建 Adapter 后恢复同一原生会话并回忆前轮内容、切换 Binding 后独立会话，以及删除原生会话文件后拒绝执行。该验收直接调用 Adapter，不声称覆盖完整跨 Bot NATS 链路、所有 provider 或其他 CLI/OS 版本。

---

## OpenClaw 接入

`OpenClawAdapter` 通过调用本地 `openclaw` CLI 将消息投递给 OpenClaw Agent。

### 消息格式

投递时把来源、消息关联、会话、payload 和 metadata 编码为 JSON prompt，字段值不再解释为可拼接的 header。JSON 结构不能消除提示词注入；授权和工具限制必须由本地策略落实。

只有配置在每 Bot `harness.allowedAgentIds` 中的命名 AgentID 才会转换为 `--agent`。保留 ID `default` 使用产品原生默认 Agent，不传 `--agent default`；未配置列表时只允许该默认目标。该列表只影响本地 CLI 路由，不进入发现或聊天参与者目录。

### 会话映射

- 正常消息的产品会话由本地 Bot、bindingRevision、产品、发送方、目标 Agent 与 sessionKey 的作用域摘要确定。
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

在该 Bot 的 `harness` 对象中设置：

```json
{
  "adapter": "webhook",
  "timeout": "10m",
  "instance": "dedicated-receiver-a",
  "webhookUrl": "https://example.com/hooks/clawsynapse/a",
  "credentialsFile": "/private/clawsynapse/webhook-a.token"
}
```

`credentialsFile` 是部署方准备的私有 0600 文件，内容作为 Webhook 接收端的 Bearer token；不把明文放进 Bot JSON。不再从全局 `WEBHOOK_AUTH_TOKEN` / `.env` 获取各 Bot 的凭据。未配置 token 时不发送 Authorization，生产部署应为每个绑定提供专用凭据和接收入口。这不是 daemon 管理凭据；先停止 Bot，再通过无 token 的本机管理请求 CAS 更新完整 config，并重新启动。

### Webhook Payload

每次消息投递时，`WebhookAdapter` 向配置的 URL 发送 `POST` 请求，`Content-Type: application/json`，body 格式：

```json
{
  "botId": "n1-2f4c6e8a0b1d3f557799aabbccddeeff",
  "bindingRevision": "binding-a",
  "instance": "dedicated-receiver-a",
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
| `botId` | string | 本地接收 Bot ID，由其 DID 自动派生 |
| `bindingRevision` / `instance` | string / string | 当前不可混用的执行绑定与专用接收实例 |
| `messageId` | string | ClawSynapse 原始消息 ID，可用于幂等去重、审计和关联回复 |
| `requestId` / `runId` / `seq` | string / string / uint64 | run 事件的原请求、Bot 执行 ID 和序号 |
| `type` | string | 消息类型（如 `chat.message`, `task.assign`） |
| `from` | string | 发送方 Bot ID |
| `sessionKey` | string | 会话标识，可能为空 |
| `message` | string | 消息正文 |
| `payload` | object | 结构化请求或执行事件；不能只消费 message |
| `metadata` | object | 附加元数据，可能为空 |

配置 `harness.credentialsFile` 后，POST 投递和 GET 健康检查都会携带：

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

推荐 Webhook 接收端直接返回结构化 JSON，例如：

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

Workspace 请求（`metadata.clawsynapseReplyMode="chat"`）使用独立 final lane：
过程 `output` frame 不作为答案。接收端在真正最终 assistant 消息确定后，输出
一个或多个 `{"type":"final","text":"最终答案的一段"}`，最后 `{"type":"completed"}` 并结束响应。
每个 final.text 沿用 64 KiB 限制；多段只承载同一最终消息，不能夹带工具轮次叙述。
daemon 等待 completed 及完整 EOF，再发布最终答案；没有 final、失败、
截断或 completed 后还有 frame 均不发布候选。非 workspace 的 output 流式语义不变；
单次 JSON completed 响应的 reply 仍被视为明确最终答案。


EOF、超限、无效 JSON/未知 frame、或建立连接后的传输中断均表示结果 indeterminate；已收到的 partial output 保留，不能当作 completed 或安全自动重跑。只有 dial 根本未建立才可按相同 messageId 重试。集成必须持久去重 queued/admitted/completed 命令：相同 messageId 不得重复启动模型或外部动作；已完成可返回持久结果，无法确认的 admitted 状态必须显式 failed/indeterminate，不能伪造成功。

普通命令包含 requestId=messageId、Bot 持久 runId 和当前 seq，反馈保持原有关联字段。run.*、chat.context 与 transfer.received 是非唤醒数据；识别的 topic-sync/topic-receipt 由 daemon 持久处理，其余 chat.context 仍可投递 Webhook 保存授权观察数据，均不产生模型 Run。文件通知在引用持久提交并物化到接收 Bot workspace 后投递；不能借其 localPath 授权其他 Bot。消息日志持久 queued/admitted/done 与 outbox；重启将崩溃前 admitted 的执行报告为 execution.interrupted/indeterminate，不重新运行；topic 会话同代际后续执行同时暂停。外部副作用不提供 exactly-once 保证。

### 执行内发布可见进展

普通 admitted peer 命令的 native JSON 和 Webhook payload 可携带本地 `executionContext`；版本化 native topic 不把它放入模型输入，只使用调用环境：
`cliPath`（已解析的绝对可执行路径）、`apiAddr`（当前 daemon 的完整 loopback HTTP(S) URL）、
`botId`、`targetBot`、`requestId`、`runId`、原始可见 `sessionKey`、可选 `agentId`，
以及 `progressCommand` argv 前缀。该前缀最后是 `--message`，只需追加一句进展；
不能从 scoped Harness Session、cwd、其他请求或默认配置推导路由。
这是本地调用元数据，不是新的 wire envelope 字段。

```bash
"$CLAWSYNAPSE_CLI" --api-addr "$CLAWSYNAPSE_API_ADDR" --bot "$CLAWSYNAPSE_BOT_ID" --json \
  publish --type chat.progress --target "$CLAWSYNAPSE_TARGET_BOT" \
  --request-id "$CLAWSYNAPSE_REQUEST_ID" --run-id "$CLAWSYNAPSE_RUN_ID" \
  --session-key "$CLAWSYNAPSE_SESSION_KEY" --message "Reading the relevant files."
# 原请求有 Agent 时另加 --agent "$CLAWSYNAPSE_AGENT_ID"
```

本地 Codex、OpenCode、pi、omp 和 OpenClaw CLI 子进程均按本次调用覆盖上述
CLAWSYNAPSE_* 环境变量（另含 CLAWSYNAPSE_AGENT_ID）。版本化 topic 的进度工具
使用这些当前值；私有 reader 能力 CLAWSYNAPSE_CONTEXT_TOKEN 只进入子进程环境
和专用 HTTP header，不进入 prompt、argv 或 wire。普通 peer/Gateway 路径仍可携带
完整 executionContext：Gateway 未必继承环境，使用其字面量 progressCommand。
CLI 优先解析 daemon 同目录的 clawsynapse，然后查真实 PATH；未安装 CLI、
不是 admitted 命令或上下文不完整时不提供 executionContext，任务仍正常执行。
Harness 必须允许执行该真实 CLI 且能访问本机 endpoint；本功能不绕过工具沙箱或权限，
报告失败不得终止主任务。没有 progress 工具权限时不伪造进展。

版本化 topic 的本地固定策略直接提供上述完整进度命令与触发条件，不依赖用户提及
ClawSynapse、`chat.progress` 或模型另行发现通信 skill。用户明确要求过程汇报时，
在开始实质工作前及真实阶段切换时发布；其他多阶段工作按有意义的阶段汇报，
快速回答可省略。普通 assistant 叙述、shell echo 和最终回复中的批量阶段总结
都不算进度投递；最终正文只保留交付物。缺少路由或工具、发布失败时继续主任务，
不声称已经报告，也不修改权限。部署环境仍需允许实际 CLI；本地策略不扩大权限。

Webhook 的 sessionKey 保持原始可见会话键。仅同机且可访问该 loopback endpoint 的
receiver 可以在当前 HTTP 调用仍 admitted 时使用 executionContext 发布进展；
异步 accepted 后或远端 receiver 不具有回调许可，不暴露远程 daemon API。
收到的 chat.progress 永远仅保留在 Bot inbox，不转发给 Webhook，也不产生反馈。

Workspace 原生输出同样采用最终消息语义，而非屏蔽特定词语：Pi/omp 选最后
stop assistant 的终态 content；toolUse assistant 及更早轮次全部舍弃。Codex 选当前
成功 turn 的最后 completed agent_message（显式 commentary phase 不作答案），
后续工具活动会作废此前候选。OpenCode 按 messageID/step 边界选最终 stop 消息，
没有 step_finish 时仍用持久会话中同一最终 assistant 的 stop/成功 idle 核验。
OpenClaw 是一次性 Gateway 响应，选最后非 reasoning/commentary 的 assistant payload，
不把多个历史 assistant payload 串接。只有整次 CLI 调用和会话保存成功后才交付
最终正文；此前不调用答案 Emit，错误或不确定结果不会将候选正文变成最终答案。



文件通知另有持久 admission：引用先提交，pending 通知只在绑定 callback 后物化 workspace 副本；callback 前持久写入 admitted。崩溃后的 admitted 通知不自动回调重放，其效果仍可能不确定；不要把持久引用存在等同于通知已被外部产品处理。读取/转发依然可按 Bot 引用查询。


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

# NanoClaw API

HTTP API 运行在 `HTTP_HOST:HTTP_PORT`（默认 `localhost:3000`）。

所有接口（除特别标注外）需要 Bearer Token 认证：

```
Authorization: Bearer <API_AUTH_TOKEN>
```

---

## POST /api/chat

向指定 group 发送消息，通过 SSE 流式接收 agent 回复。

### 请求

```
POST /api/chat
Content-Type: application/json
Authorization: Bearer <token>
```

```json
{
  "prompt": "你好，帮我查一下天气",
  "groupId": "my-group"
}
```

| 字段      | 类型   | 必填 | 说明                                                    |
| --------- | ------ | ---- | ------------------------------------------------------- |
| `prompt`  | string | 是   | 发送给 agent 的消息内容                                 |
| `groupId` | string | 否   | 目标 group 的 folder 名。省略则使用 `MAIN_GROUP_FOLDER` |

### 响应

返回 `text/event-stream`（SSE），包含以下事件类型：

**`message`** — agent 的回复片段（可能有多条）

```
event: message
data: {"text":"今天北京天气晴，气温 15°C。"}
```

**`error`** — agent 执行出错

```
event: error
data: {"error":"Container timeout"}
```

**`done`** — 本轮对话结束

```
event: done
data: {"sessionId":"abc123"}
```

### 错误码

| 状态码 | 说明                           |
| ------ | ------------------------------ |
| 400    | 缺少 `prompt` 或 JSON 格式错误 |
| 401    | 未认证或 token 无效            |
| 409    | 该 group 已有一个进行中的请求  |
| 413    | 请求体超过大小限制             |

### 注意事项

- 同一 group 同时只允许一个活跃 SSE 连接，并发请求返回 `409`
- 如果 `groupId` 对应的 group 不存在，会自动注册
- 客户端断开连接后，相关资源会自动清理

---

## POST /api/groups/:groupId/memory

更新指定 group 的记忆文件，写入到 `groups/<groupFolder>/CLAUDE.md`。

### 请求

```
POST /api/groups/my-group/memory
Content-Type: application/json
Authorization: Bearer <token>
```

```json
{
  "content": "# My Group Memory\n\n- 用户偏好：回复简短。"
}
```

| 字段      | 类型   | 必填 | 说明                                      |
| --------- | ------ | ---- | ----------------------------------------- |
| `content` | string | 是   | 将写入 `CLAUDE.md` 的完整内容（覆盖写入） |

### 响应

```json
{
  "status": "ok",
  "groupId": "my-group",
  "path": "groups/my-group/CLAUDE.md",
  "bytes": 58
}
```

### 错误码

| 状态码 | 说明                                            |
| ------ | ----------------------------------------------- |
| 400    | `groupId` 非法、缺少 `content` 或 JSON 格式错误 |
| 401    | 未认证或 token 无效                             |
| 413    | 请求体超过大小限制                              |
| 500    | 文件写入失败                                    |

### 注意事项

- `groupId` 会按系统规则标准化（仅保留字母、数字、`_`、`-`，并转小写）后作为实际 folder 名
- 会自动创建 `groups/<groupFolder>/` 目录（若不存在）
- 每次调用都会覆盖写入整个 `CLAUDE.md`
- 该接口只写文件，不会自动注册 group（不会等价于 `POST /api/groups`）

---

## POST /api/pilot/webhook

Pilot Protocol 事件回调。仅接受 localhost 请求，无需 Bearer Token。

### 请求

```
POST /api/pilot/webhook
Content-Type: application/json
```

```json
{
  "event": "message.received",
  "node_id": 42,
  "timestamp": "2026-02-28T10:00:00Z",
  "data": {
    "peer_node_id": 7,
    "message": "Hello from node 7"
  }
}
```

| 字段        | 类型   | 必填 | 说明                            |
| ----------- | ------ | ---- | ------------------------------- |
| `event`     | string | 是   | 事件类型                        |
| `node_id`   | number | 否   | 来源节点 ID                     |
| `timestamp` | string | 否   | 事件时间戳                      |
| `data`      | object | 否   | 事件数据，内容取决于 event 类型 |

### 支持的事件类型

| event                               | data 字段                             | 生成的 prompt                                                                 |
| ----------------------------------- | ------------------------------------- | ----------------------------------------------------------------------------- |
| `message.received` / `data.message` | `peer_node_id`, `message` / `content` | `[Pilot Protocol] 收到来自 node {peer} 的消息: {content}`                     |
| `data.file`                         | `peer_node_id`                        | `[Pilot Protocol] 收到来自 node {peer} 的文件，请运行 pilotctl received 查看` |
| `handshake.received`                | `peer_node_id`, `justification`       | `[Pilot Protocol] node {peer} 请求建立信任连接，理由: {justification}`        |

其他事件类型会被忽略（返回 200 但不触发 agent）。

### 响应

```json
{ "status": "ok" }
```

### 注意事项

- **仅限 localhost**：非本地请求返回 `403`
- 所有事件路由到 **主群组**（`MAIN_GROUP_FOLDER`）
- 由 `pilotctl set-webhook` 在启动时自动注册

---

## Task Completion Webhook（出站）

定时任务完成后，NanoClaw 主动向外部服务发送通知。通过环境变量 `TASK_COMPLETION_WEBHOOK_URL` 配置。

### 请求（NanoClaw → 外部服务）

```
POST <TASK_COMPLETION_WEBHOOK_URL>
Content-Type: application/json
```

```json
{
  "taskId": "task-001",
  "groupFolder": "my-group",
  "chatJid": "my-group",
  "scheduleType": "cron",
  "scheduleValue": "0 9 * * *",
  "durationMs": 12500,
  "runAt": "2026-02-28T09:00:00+08:00",
  "nextRun": "2026-03-01T09:00:00+08:00",
  "status": "active",
  "success": true,
  "resultSummary": "任务执行完成，已发送日报。",
  "chatOutput": "NanoClaw: 任务执行完成，已发送日报。",
  "error": null
}
```

| 字段            | 类型                                 | 说明                                                                                    |
| --------------- | ------------------------------------ | --------------------------------------------------------------------------------------- |
| `taskId`        | string                               | 任务 ID                                                                                 |
| `groupFolder`   | string                               | 所属 group 的 folder 名                                                                 |
| `chatJid`       | string                               | 关联的 chat ID                                                                          |
| `scheduleType`  | `"cron"` \| `"interval"` \| `"once"` | 调度类型                                                                                |
| `scheduleValue` | string                               | cron 表达式、毫秒间隔或 ISO 时间戳                                                      |
| `durationMs`    | number                               | 本次执行耗时（毫秒）                                                                    |
| `runAt`         | string                               | 本次执行时间（ISO）                                                                     |
| `nextRun`       | string \| null                       | 下次执行时间，`once` 类型完成后为 null                                                  |
| `status`        | string                               | 任务当前状态                                                                            |
| `success`       | boolean                              | 本次执行是否成功                                                                        |
| `resultSummary` | string                               | 执行结果摘要                                                                            |
| `chatOutput`    | string \| null                       | 实际发送到聊天流的完整内容（包含工具 `send_message` 与 agent 输出，多段消息用换行拼接） |
| `error`         | string \| null                       | 错误信息，成功时为 null                                                                 |

### 注意事项

- 未配置 `TASK_COMPLETION_WEBHOOK_URL` 时不发送
- 请求超时 10 秒
- 发送失败仅记录 warn 日志，不重试
- **仅用于定时任务**，`POST /api/chat` 的普通对话回复不会触发此 webhook

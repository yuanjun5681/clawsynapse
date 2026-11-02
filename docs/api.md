---
summary: "ClawSynapse daemon v2 管理、Bot 业务与文件 API"
title: "ClawSynapse API Reference"
---

# ClawSynapse API Reference

本地默认地址为 `http://127.0.0.1:18080`。daemon 使用 schemaVersion 3 的 `config.json`；catalog 和 Bot 导出 manifest 同为版本 3，journal、Session 与 wire 不因 Role 升版。配置、目录和离线迁移见 [operations.md](operations.md)，命令见 [cli.md](cli.md)。本地 HTTP API 与网络协议各自版本化：网络签名封套、subject 和 `from`/`to` 仍使用 [wire v2](protocol.md)，本地路由和字段不再使用 v1。

## 访问与响应

daemon 只监听 loopback；无 `Authorization` 的本机请求可完整管理所有 Bot，不需要管理员凭据。显式提交错误或格式非法的 token 返回 **401** `unauthorized`，Bot scoped token 权限不足返回 **403** `forbidden`。

- 每个 Bot 仍有独立 `DataDir/bots/{botId}/credentials/api.token`（0600）。显式使用它时，只能读取自己的 Bot 信息、查询和操作自己的业务状态，不能列举 daemon/Bot catalog、读取或修改配置、管理生命周期、导入宿主机文件，也不能访问其他 Bot。
- 作用域凭据是可选的细粒度调用方式，不是本机进程隔离：任何本机进程都能省略 token 取得完整管理能力。不要将本地 API 代理或转发给不可信调用方。
- 不把 Bot 凭据放入 URL、配置 JSON、日志或版本库。客户端拒绝所有非 loopback endpoint（包括 HTTPS）及重定向；跨设备 Bot 通信使用 NATS，不是远程 daemon 管理。Bot 间身份、签名、认证与信任检查不变。
- HTTP 路径必须包含完整 Bot ID；没有默认 Bot、无 Bot 路径或 `/v1/*` 兼容别名。未知路由为 404，方法不匹配为 405。
- JSON body 最多 256 KiB，必须是单个对象；未知字段、重复键、非法编码、尾随对象返回 400 `invalid_argument`。

除文件字节响应外，业务接口返回 JSON `APIResult`：

```json
{"ok":true,"code":"bot.info","data":{},"ts":1710000000000}
```

`message` 是可选的人类可读说明，`code` 为机器码，`ts` 为 Unix 毫秒。未分类内部故障为 500 `daemon.failed`。daemon 关停时拒绝写入，返回 503 `daemon.stopping`。

## daemon 管理（本机无 token）

| 方法与路径 | 成功状态 / code | data |
|---|---|---|
| `GET /v2/health` | 200 `daemon.healthy` | `activeBots`, `failedBots`, `botsCount`, `version`, `recoveryIssues`, `capabilities: ["console","workspace_binding"]` |
| `GET /v2/bots` | 200 `bots.list` | `items: BotInfo[]`，按 botId 排序 |
| `POST /v2/bots` | 201 `bot.created` | `bot: BotInfo` |
| `POST /v2/workspaces/{workspaceId}/binding` | 200 `workspace.bound` | `bot: BotInfo`, `token: string`（该 Bot 的 scoped token） |
| `GET /v2/bots/{botId}` | 200 `bot.info` | `bot: BotInfo`；自己的 scoped token 也可读取 |
| `GET /v2/bots/{botId}/config` | 200 `bot.config` | `config: BotConfig` |
| `PUT /v2/bots/{botId}/config` | 200 `bot.updated` | `bot: BotInfo` |
| `POST /v2/bots/{botId}/start` | 202 `bot.start` | `bot: BotInfo` |
| `POST /v2/bots/{botId}/stop` | 202 `bot.stop` | `bot: BotInfo` |
| `POST /v2/bots/{botId}/archive` | 200 `bot.archive` | `bot: BotInfo` |
| `POST /v2/bots/{botId}/restore` | 200 `bot.restore` | `bot: BotInfo` |

`BotInfo` 包含 `botId`, `did`, `displayName`, `role`, `desiredState`, `state`, `archived`, `revision`, `bindingRevision`, `networkHealthy`, `harnessHealthy`, `harnessApplicable`，失败时有 `error`。Console 的 `harnessApplicable=false`、`harnessHealthy=false` 表示不适用，而非执行故障；Agent 的 Harness 为 applicable。catalog 列表不主动探测 Harness；读取单个运行 Agent 信息才查询 Harness 健康。daemon health 不等于任何模型任务已验收。

创建接受 BotConfig；必须显式提供单值 `role: "console"|"agent"`，缺失、空值、`null`、未知 Role、`roles[]` 与尚未实现的 `device` 均拒绝。`displayName` 必填。Console 必须省略 `harness`（含 `null`、`{}`）与执行预算 `maxConcurrent`/`maxQueued`；Agent 必须显式提供非空 `harness` 和受支持的 `harness.adapter`。先检查字段适用性，再按角色补齐其他默认值，不推断 Role 或 adapter。身份由新建 Ed25519 密钥派生，不能由调用方选择 botId。创建后为 stopped，不自动启动。Agent 示例：

```json
{"displayName":"assistant-a","role":"agent","harness":{"adapter":"opencode","model":"zai-coding-plan/glm-5.3-flash","timeout":"10m"}}
```

Console 示例：`{"displayName":"inbox","role":"console"}`。Role 不是管理权限，所有角色仍受既有凭据、信任与文件授权约束。

工作区绑定接受 `{"displayName":"Bot-A"}`；`workspaceId` 为客户端持久保存的 1–128 位 ASCII 字母、数字、`-` 或 `_`。首次创建独立 Console Bot（显式信任、stopped），在 catalog 中原子认领；同一工作区并发重试、响应丢失或 daemon 重启均返回同一身份及原 scoped token，不按名称/labels 复用 Bot，也不会因重试名称变化而改名。客户端应持久保存 workspaceId，以无 token 的本机管理请求启动并等待 Bot running，之后可选用 scoped token 查询/发布。响应 `Cache-Control: no-store`；不要记录或展示 token。

绑定归档、失败、密钥/凭据/标记缺失或 catalog 认领丢失时返回 409 `workspace.binding_invalid`，不自动换钥或另建身份。恢复原数据后重试；归档须显式 restore。工作区 Bot 固定为 `role=console`，不配置 Harness；工作区绑定不是 Harness Binding。工作区绑定通过无 token 的本机管理请求调用，显式 scoped token 不可认领其他工作区。

配置更新是**完整替换 + revision CAS**：先 GET `data.config`，修改后原样 PUT，必须提交当前 `revision` 和原 `role`。缺失 Role 或普通更新切换 Role 均拒绝，即使同时调整 Harness 也不允许。运行/过渡中更新返回 409 `bot.must_stop`；revision 过期为 409 `bot.revision_conflict`。成功递增 revision；Agent 的 `bindingRevision` 由服务器控制，Harness 绑定变更会产生新绑定和独立工作区/产品会话，纯 schema 迁移保留原修订。Console 不构造执行 Binding；旧 bindingRevision 可保留为历史文件路径元数据。校验失败为 400 `bot.config_invalid`。完整字段、默认值见 operations。

生命周期请求无须 body。start/stop 是异步接纳，轮询 Bot 信息直到 running/stopped/failed；202 不表示已经就绪。状态有 `stopped|starting|running|stopping|failed|archived`。archive 必须先停止并等待，保留身份、状态、文件引用；restore 只恢复到 stopped。未知 Bot 为 404 `bot.not_found`；归档 Bot 启动为 409 `bot.archived`；冲突为 409 `bot.transition_conflict` / `bot.must_stop`。OpenClaw/Webhook 缺少明确 instance 为 400 `bot.binding_invalid`，未归档 Bot 共享同种 adapter 的 instance 为 409 `bot.binding_shared`。

## Bot 业务路由

下表 `B` **只为文档缩写**，代表 `/v2/bots/{botId}`，不是实际 URL。所有写操作要求 Bot running；否则 409 `bot.must_start`。starting/stopping 时读业务也返回该错误。stopped/archived 可通过持久状态读取消息、请求、信任、备注和文件记录/字节；不建立网络连接、不执行 Harness。

| 方法 | 路径 | 成功 / 用途 |
|---|---|---|
| GET | `B/health` | 200 `health.ok`；所选 Bot 的 self/nats/adapter 健康 |
| GET | `B/peers` | 200，当前发现列表；`data.items` |
| GET | `B/peers/{peerBotId}/attrs` | 200 `peer_attrs.ok`；`data.attrs` |
| PUT | `B/peers/{peerBotId}/attrs` | 200 `peer_attrs.ok`；patch 备注 |
| DELETE | `B/peers/{peerBotId}/attrs` | 200 `peer_attrs.ok`；删除全部备注 |
| POST | `B/auth/challenge` | 200 `auth.challenge_accepted` |
| GET | `B/trust` | 200 `trust.snapshot` |
| GET | `B/trust/pending` | 200 `trust.pending`；`data.items` |
| POST | `B/trust/request` | 200 `trust.requested` |
| POST | `B/trust/approve` | 200 `trust.responded` |
| POST | `B/trust/reject` | 200 `trust.responded` |
| POST | `B/trust/revoke` | 200 `trust.revoked` |
| POST | `B/publish` | 200 `msg.published` |
| GET | `B/messages` | 200 `msg.recent` 或 `msg.replay` |
| GET | `B/requests/{messageId}` | 200 `msg.request` |
| POST | `B/transfer/send` | 200 `transfer.sent` |
| POST | `B/transfer/import` | 201 `transfer.imported`；本机管理，不带 scoped token |
| GET | `B/transfers` | 200 `transfer.list`；`data.items` |
| GET | `B/transfer/{transferId}` | 200 `transfer.detail`；`data.transfer` |
| GET | `B/transfer/{transferId}/file` | 200 文件字节，不是 APIResult |
| POST | `B/transfer/{transferId}/forward` | 200 `transfer.sent` |
| DELETE | `B/transfer/{transferId}` | 200 `transfer.deleted` |

### 发现、备注与信任

`GET B/health` 返回 `data.self`（botId、did、role、version、identityFingerprint、trustMode、displayName、labels）、peersCount、nats（连接状态、连接/断连/重连时间、次数、消息/字节计数、lastError）、adapter（applicable、name、healthy、degraded、detail，可能有 error）。Console 为 `applicable=false`，没有 Harness 探针；不将 `healthy=false` 解释为执行故障。stopped 状态的网络/执行探针不可用，不伪装运行；HTTP 200 是查询成功，不保证 Harness healthy 或模型任务完成。

本地身份字段使用 `botId`，目标参数为 `targetBot`；旧 `targetNode` 被严格解码拒绝。peer 的显示名与自报 `labels` 来自签名 discovery，但不授予权限。`GET peers` 支持重复 `label=key=value`（标量精确匹配）、`labelHas=key=value`（数组成员）、`name=substring`（显示名/本地别名，大小写不敏感）。

`GET B/peers` 的每个 peer 可包含平铺的 `harnessAdapter`、`harnessModel` 字符串，来自最近一次有效签名 discovery 的 `metadata.harnessAdapter` / `metadata.harnessModel` 当前 Binding 配置；`agentProduct` 仍是产品标识。Console、旧对端或未配置模型时，相应字段省略，客户端应显示“未声明”而非推断模型。新声明缺失或非字符串的键会清除旧值，Binding 更新后的重启会公布新配置。这不是远端健康、可执行性或实际 Run 模型的证明，不附带 instance、路径、凭据或 Binding 修订；不能据此自动授权或绕过信任。

备注 patch 是 `{"alias":"现场网关","description":"上海机房","labels":{"floor":"3"}}`。省略保持，空字符串/空对象清除；labels 整体替换。备注按本地 Bot 隔离，只影响显示，不影响远端身份和授权。非法 patch 为 400；持久化失败为 500 `peer_attrs.persist_failed`。

认证 body 为 `{"targetBot":"n1-…"}`；trust request 为 `{"targetBot":"n1-…","reason":"协作","capabilities":["chat"]}`；approve/reject 为 `{"requestId":"req-…","reason":"已确认"}`；revoke 为 `{"targetBot":"n1-…","reason":"撤销"}`。认证和信任是两个步骤；正常模式先握手，再请求并批准信任。业务错误返回 400，code 为对应的 `auth.challenge_failed` / `trust.request_failed` / `trust.response_failed` / `trust.revoke_failed`。`GET trust` 返回 `trusted`, `rejected`, `revoked`, `pending`；每个本地 Bot 独立保存信任。

### 发布、回放与执行状态

```json
{
  "targetBot":"n1-11223344556677889900aabbccddeeff",
  "type":"task.assign",
  "payload":{"instruction":"汇总报告","input":{"period":"today"}},
  "sessionKey":"report-session",
  "idempotencyKey":"report-2026-10-31",
  "metadata":{"priority":"high"}
}
```

`targetBot` 必填；type 默认 chat.message；`message` 是 payload.text 的快捷输入，与 `payload` 互斥。可选 `agentId` 只能选择接收 Bot 本地 Harness allowlist 内的目标；不是聊天联系人，也不能选择另一个 Bot 或模型。`expiresAt` 为 Unix 毫秒，默认/最大有效期 10 分钟。

成功 `data` 包含 `targetBot`, `messageId`, `sessionKey`，仅表示 JetStream 已确认持久接纳，不代表执行完成。发布先写本 Bot 持久 outbox；网络失败可能返回 400 `msg.publish_failed` 但 intent 仍待发送。不得改新 ID/新 key 重试副作用。HTTP 调用方应提供 ≤512 字节 `idempotencyKey`：同 key/相同规范化内容跨重启返回同一 messageId，同 key 不同内容为 409 `msg.idempotency_conflict`。未指定 sessionKey/expiresAt 在第一次持久化生成；重试提交相同请求。禁止伪造 `run.*`（403 `control.unauthorized`）。

`GET B/messages` 默认最近 100 条，可用 `limit=1..1000`。`?after=0&limit=100` 按持久 cursor 升序回放。`data` 为 `{items:[{...完整wire封套,cursor}],cursor}`，空页保持 after；cursor 是本 Bot 本地收件箱序号，不属于签名或 stream 序号。按 Bot 分别持久保存最后成功消费游标。

`GET B/requests/{messageId}` 查询本 Bot 发布记录，未知 ID 为 404 `msg.not_found`。`data` 包含完整 `message`, `publication: pending|published`, `status: sending|sent|running|accepted|completed|failed|indeterminate`, `events`。events 仅关联同一请求、对端、Agent 和会话。accepted 不是完成；indeterminate 不可自动重跑。执行事件语义及恢复见 [messaging.md](messaging.md)。

### 文件：引用授权，不暴露全局 blob 路径

文件字节按内容去重，但授权依据是所选 Bot 持久文件引用；知道 checksum、transferId 或其他 Bot 的记录不授予读取/转发权限。列表和详情不返回全局 blob 路径。所有操作必须走 Bot-qualified 路径：

1. Bot workspace 内文件：`POST B/transfer/send`，body `{"targetBot":"n1-…","filePath":"report.pdf","mimeType":"application/pdf","metadata":{"taskId":"task-1"}}`。路径必须在当前 Bot/绑定 workspace 内，不能用其他 Bot 或任意宿主路径。
2. 宿主文件：本机无 token 调用 `POST B/transfer/import`，body `{"filePath":"/absolute/host/report.pdf"}`。成功返回 `data.transfer`，为该 Bot 建立持久引用；显式 scoped credential 返回 403。
3. 已授权引用：`POST B/transfer/{transferId}/forward`，body `{"targetBot":"n1-…"}`（可选 mimeType/metadata）。接收方获得自己的引用，不共享来源 Bot 的授权。
4. 查看：GET transfers/transfer；记录包括 transferId、方向、文件名/大小、MIME、checksum、状态、时间和 metadata。按 Bot 持久化，重启后可查询。
5. 读取：`GET B/transfer/{transferId}/file` 返回 `application/octet-stream`，`Cache-Control: no-store`、`X-Content-Type-Options: nosniff`。未知/未授权引用为 404 `transfer.not_found`；已授权但字节缺失/损坏为 500 `transfer.file_unavailable`。
6. 删除：`DELETE B/transfer/{transferId}` 仅删除该 Bot 的引用/记录，不撤销其他 Bot 的引用，也不立即删除仍被引用的共享内容。成功返回 `data.transferId`。未知/删除失败为 400 `transfer.delete_failed`。

send/forward 成功 `data` 为 `{transferId,bucket,size,checksum}`，表示传输发起，不是远端模型完成。import/send/forward 校验、配额或存储失败分别为 400 `transfer.import_failed` / `transfer.send_failed` / `transfer.forward_failed`。发送/删除的运行服务不可用时为 503 `transfer.disabled`。GC、逻辑配额与物理容量见 operations。

```bash
# 使用所选 Bot 的完整 ID 与其 scoped token 读取文件
curl --fail -H "Authorization: Bearer $(cat "$TOKEN_FILE")" \
  "http://127.0.0.1:18080/v2/bots/$BOT_ID/transfer/$TRANSFER_ID/file" -o report.pdf
```

## Go 客户端

```go
c := api.NewClient("127.0.0.1:18080", 5*time.Second,
    api.WithTokenFile(tokenPath), api.WithBot(botID))
result, err := c.Post(ctx, "/publish", map[string]any{
    "targetBot": peerBotID, "message": "hello", "idempotencyKey": requestKey,
})
```

`WithBot` 将业务相对路径限定到 `/v2/bots/{botId}`，并拒绝越过所选 Bot 作用域。daemon 管理使用不带 WithBot、不配置 token 的客户端和明确 `/v2/*` 路径。客户端自动处理 JSON；HTTP >=400 返回 error，并保留 APIResult。`WithTokenFile` 只用于可选 Bot scoped 调用，不自动读取管理员凭据。所有 endpoint 必须是 loopback；远端 HTTPS 也不支持。

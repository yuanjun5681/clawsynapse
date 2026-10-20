---
summary: "ClawSynapse 本地 HTTP API 完整接口参考"
title: "ClawSynapse API Reference"
---

# ClawSynapse API Reference

最后更新：2026-10-20

`clawsynapsed` 在本地暴露 HTTP API，供 Agent、CLI 或外部系统调用。默认监听 `127.0.0.1:18080`，可通过 `LOCAL_API_ADDR` 或 `--local-api-addr` 配置。

## 通用约定

### 访问保护与请求限制

所有路由，包括 health/config，均要求 `Authorization: Bearer <token>`，缺失或错误返回 HTTP 401。首次启动自动生成 `DataDir/api.token`，文件权限必须为 0600；可用 `API_TOKEN_PATH`／`--api-token-path`／`apiTokenPath` 指定路径。不要把 token 放入 URL、日志或版本库。CLI 与 Go Client 自动读取本地配置中的 token 文件。

非 loopback 监听必须显式配置 `allowRemoteAPI: true`，同时提供 `apiTLSCertFile` 和 `apiTLSKeyFile`，实际以 HTTPS 启动；客户端拒绝远端明文和重定向，证书须由客户端信任的 CA 签发。

请求 body 最多 256 KiB，必须是单个 JSON 对象；未知结构字段、重复键、尾随对象和非法编码返回 400。


### 统一响应格式

所有接口返回 `Content-Type: application/json`，统一使用 `APIResult` 结构：

```json
{
  "ok": true,
  "code": "模块.动作",
  "message": "人类可读的描述",
  "data": {},
  "ts": 1710000000000
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `ok` | bool | 操作是否成功 |
| `code` | string | 机器可读的结果码，格式 `模块.动作`（如 `auth.challenge_accepted`） |
| `message` | string | 人类可读的结果描述 |
| `data` | object | 业务数据，失败时可能包含上下文信息 |
| `ts` | int64 | 响应时间戳，Unix 毫秒 |

### 错误处理

请求参数错误返回 `400 Bad Request`，响应中 `ok: false`：

```json
{
  "ok": false,
  "code": "invalid_argument",
  "message": "invalid json payload",
  "ts": 1710000000000
}
```

---

## 接口列表

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/v1/health` | 健康检查 |
| `GET` | `/v1/peers` | 获取已发现的 peer 列表 |
| `GET` | `/v1/peers/{nodeID}/attrs` | 读取指定节点的本地备注 |
| `PUT` | `/v1/peers/{nodeID}/attrs` | 更新指定节点的本地备注（patch） |
| `DELETE` | `/v1/peers/{nodeID}/attrs` | 删除指定节点的全部本地备注 |
| `GET` | `/v1/config` | 获取当前启动配置快照 |
| `PUT` | `/v1/config` | 校验并保存配置文件（需重启生效） |
| `POST` | `/v1/publish` | 向目标节点发布消息 |
| `GET` | `/v1/messages` | 获取最近收到的消息 |
| `POST` | `/v1/auth/challenge` | 向目标节点发起认证握手 |
| `GET` | `/v1/trust/pending` | 获取待处理的信任请求 |
| `POST` | `/v1/trust/request` | 向目标节点发起信任请求 |
| `POST` | `/v1/trust/approve` | 批准信任请求 |
| `POST` | `/v1/trust/reject` | 拒绝信任请求 |
| `POST` | `/v1/trust/revoke` | 撤销对目标节点的信任 |
| `POST` | `/v1/transfer/send` | 向目标节点发送文件 |
| `GET` | `/v1/transfers` | 获取当前传输记录列表 |
| `GET` | `/v1/transfer/{transferId}` | 获取单个传输详情 |
| `DELETE` | `/v1/transfer/{transferId}` | 删除传输记录并尝试清理 JetStream 对象 |

---

## GET /v1/health

健康检查，返回当前节点身份、服务状态、NATS 连接信息以及本地 Agent Adapter 状态。

**响应**

```json
{
  "ok": true,
  "code": "health.ok",
  "message": "service healthy",
  "data": {
    "self": {
      "nodeId": "n1-2f4c6e8a0b1d3f557799aabbccddeeff",
      "did": "did:key:z6MkexampleLocalDid",
      "identityFingerprint": "sha256:1234abcd5678ef90",
      "trustMode": "tofu",
      "displayName": "边缘节点-01",
      "labels": {
        "env": "production",
        "caps": ["chat", "tools"]
      }
    },
    "peersCount": 3,
    "adapter": {
      "name": "openclaw",
      "healthy": true
    },
    "nats": {
      "name": "clawsynapsed-n1-2f4c6e8a0b1d3f557799aabbccddeeff",
      "serverUrl": "nats://220.168.146.21:9414",
      "connected": true,
      "status": "CONNECTED",
      "connectedAt": 1710000000000,
      "lastDisconnectAt": 0,
      "lastReconnectAt": 0,
      "disconnects": 0,
      "reconnects": 0,
      "lastError": "",
      "inMsgs": 1024,
      "outMsgs": 512,
      "inBytes": 65536,
      "outBytes": 32768
    }
  },
  "ts": 1710000000000
}
```

`data.self` 字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `nodeId` | string | 当前节点 ID，由本地 DID 自动派生 |
| `did` | string | 当前节点 DID，当前格式为 `did:key` |
| `identityFingerprint` | string | 当前身份公钥指纹 |
| `trustMode` | string | 当前信任模式（`open`, `tofu`, `explicit`） |
| `displayName` | string | 当前节点对外广播的可读名称（未配置时省略） |
| `labels` | object | 当前节点对外广播的 labels（含保留键 `caps`；未配置时省略） |

`data.nats` 字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | NATS 连接名称 |
| `serverUrl` | string | 当前连接的服务器地址 |
| `connected` | bool | 是否已连接 |
| `status` | string | 连接状态（`CONNECTED`, `DISCONNECTED`, `CLOSED` 等） |
| `connectedAt` | int64 | 连接建立时间，Unix 毫秒 |
| `lastDisconnectAt` | int64 | 最后一次断开时间 |
| `lastReconnectAt` | int64 | 最后一次重连时间 |
| `disconnects` | int64 | 断开次数 |
| `reconnects` | int64 | 重连次数 |
| `lastError` | string | 最后一次错误信息 |
| `inMsgs` | uint64 | 接收消息数 |
| `outMsgs` | uint64 | 发送消息数 |
| `inBytes` | uint64 | 接收字节数 |
| `outBytes` | uint64 | 发送字节数 |

`data.adapter` 字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 当前启用的 adapter 名称 |
| `healthy` | bool | adapter 健康状态 |
| `error` | string | 最近一次状态检查错误；健康时通常省略 |

---

## GET /v1/peers

获取已发现的 peer 节点列表。

**查询参数（均可选）**

| 参数 | 说明 |
|------|------|
| `label` | 形如 `key=value`，精确匹配值为字符串的 label；可重复 |
| `labelHas` | 形如 `key=value`，匹配字符串数组 label 的成员；可重复 |
| `name` | 对 `displayName`/`localAlias` 做大小写不敏感的子串匹配 |

`label`（字符串精确匹配）与 `labelHas`（数组成员匹配）是两种不同的语义，不做逗号拆分。参数格式非法时返回 400，`code` 为 `peers.invalid_filter`。

**响应**

```json
{
  "ok": true,
  "code": "peers.ok",
  "message": "peers fetched",
  "data": {
    "items": [
      {
        "nodeId": "n1-11223344556677889900aabbccddeeff",
        "did": "did:key:z6MkexamplePeerDid",
        "agentProduct": "openclaw",
        "version": "2026.3.9",
        "displayName": "上海现场网关",
        "labels": {
          "env": "production",
          "caps": ["chat", "tools"]
        },
        "localAlias": "现场网关",
        "localDescription": "上海机房",
        "localLabels": {
          "floor": "3"
        },
        "inbox": "clawsynapse.msg.n1-11223344556677889900aabbccddeeff.inbox",
        "authStatus": "authenticated",
        "trustStatus": "trusted",
        "lastSeenMs": 1710000000000,
        "metadata": { "publicKey": "base64-raw-url-public-key", "ttlMs": 30000 }
      }
    ]
  },
  "ts": 1710000000000
}
```

`data.items[]` 字段说明（Peer 结构）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `nodeId` | string | 节点 ID，由 DID 自动派生 |
| `did` | string | 节点规范身份，当前为 `did:key` |
| `agentProduct` | string | Agent 产品标识（如 `openclaw`） |
| `version` | string | Agent 版本号 |
| `displayName` | string | 远端节点自报的可读名称，来自带签名的 discovery announce |
| `labels` | object | 远端节点自报的属性（值为字符串或字符串数组）；保留键 `caps` 为字符串数组，表示自报能力集合，不是就绪状态或授权 |
| `localAlias` | string | 本地备注别名（仅本节点维护，远端不可控制） |
| `localDescription` | string | 本地备注描述（仅本节点维护） |
| `localLabels` | object | 本地备注 labels（仅本节点维护，不受远端心跳覆盖） |
| `inbox` | string | 节点 inbox subject |
| `authStatus` | string | 认证状态：`unknown`, `seen`, `auth_pending`, `authenticated`, `rejected`, `expired` |
| `trustStatus` | string | 信任状态：`none`, `pending`, `trusted`, `rejected`, `revoked` |
| `lastSeenMs` | int64 | 最后一次心跳时间，Unix 毫秒 |
| `metadata` | object | 附加元数据 |

`local*` 字段是本地备注，来自本节点的 `peer-attrs.json`，不随 announce 传播，也不被远端心跳覆盖。

---

## GET /v1/peers/{nodeID}/attrs

读取指定节点的本地备注（alias、description、labels）。

**响应**

```json
{
  "ok": true,
  "code": "peer_attrs.ok",
  "message": "peer attrs fetched",
  "data": {
    "nodeId": "n1-11223344556677889900aabbccddeeff",
    "attrs": {
      "alias": "现场网关",
      "description": "上海机房",
      "labels": {
        "floor": "3"
      }
    }
  },
  "ts": 1710000000000
}
```

`attrs` 中未设置的字段省略；对尚未记录备注（含离线或未知）的 nodeID 同样返回 200 与空 `attrs` 对象。

---

## PUT /v1/peers/{nodeID}/attrs

更新本地备注。请求体是 patch：省略的键保持原值，显式给出空值（`""`、`{}`）则清除该键。`labels` 是整体替换（不做逐键合并）；需要保留现有键时先读取再合并提交（CLI `peer label`/`unlabel` 即如此实现）。

**请求**

```json
{
  "alias": "现场网关",
  "labels": {
    "floor": "3",
    "zone": ["a", "b"]
  }
}
```

**响应**

成功返回 200，`code` 为 `peer_attrs.ok`，`data.attrs` 为更新后的完整备注。校验失败（非法 nodeID、非法 JSON、非法 label 或别名）返回 400，`code` 为 `peer_attrs.invalid`；持久化失败返回 500，`code` 为 `peer_attrs.persist_failed`（内存中的修改保留）。

---

## DELETE /v1/peers/{nodeID}/attrs

删除指定节点的全部本地备注。成功返回 200，`code` 为 `peer_attrs.ok`；持久化失败返回 500，`code` 为 `peer_attrs.persist_failed`。

备注按 NodeID 持久化在 `DataDir/peer-attrs.json`（0600，原子写入），跨重启和节点重新发现保留，仅本节点可见，不影响身份与信任判断。

---

## GET /v1/config

返回 daemon 启动时加载的配置快照，修改文件后需重启 daemon 才会反映新值。`WebhookAuthToken`（`json:"-"`）不会包含在响应中。

**响应**

```json
{
  "ok": true,
  "code": "config.ok",
  "message": "config fetched",
  "data": {
    "config": {
      "natsServers": ["nats://127.0.0.1:4222"],
      "localApiAddr": "127.0.0.1:18080",
      "displayName": "边缘节点-01",
      "labels": {
        "env": "production",
        "caps": ["chat", "tools"]
      },
      "trustMode": "tofu"
    }
  },
  "ts": 1710000000000
}
```

## PUT /v1/config

请求体为完整的配置文件结构（与 YAML 配置同名的 JSON 字段），服务端执行校验后以原子方式覆盖配置文件。成功只表示文件已保存，运行中的 discovery 等状态不变；需重启 daemon 生效。

**响应**

成功返回 200，`code` 为 `config.saved`，`message` 提示需要重启。JSON 非法返回 400 `invalid_argument`；配置校验失败返回 400 `config.invalid`；配置文件路径不可用返回 500 `config.no_path`；写入失败返回 500 `config.save_failed`。

---

## POST /v1/publish

向目标节点发布 v2 结构化消息。成功仅表示当前 Core NATS 发布调用成功，不表示对端持久接纳或 Agent 完成。连接断开时拒绝发布，不积压待重连执行的命令。

**请求**

```json
{
  "targetNode": "n1-11223344556677889900aabbccddeeff",
  "type": "chat.message",
  "agentId": "agent-42",
  "payload": {"instruction": "请汇总最新报告", "input": {"period": "today"}},
  "sessionKey": "nats:n1-localnodeid:n1-11223344556677889900aabbccddeeff",
  "metadata": { "priority": "high" }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `targetNode` | string | 是 | 目标节点 ID，由对端 DID 自动派生 |
| `type` | string | 否 | 消息类型（如 `chat.message`, `task.assign`） |
| `agentId` | string | 否 | 目标本地授权列表内的 Agent；Codex 不支持指定 Agent |
| `message` | string | 否 | 文本快捷输入，映射为 payload.text；与 payload 互斥 |
| `payload` | object | 否 | text 或 instruction/input 等结构化正文 |
| `expiresAt` | int64 | 否 | Unix 毫秒有效期；默认 10 分钟，最长发送时间起 10 分钟 |
| `sessionKey` | string | 否 | 会话标识，用于关联上下文 |
| `metadata` | object | 否 | 附加元数据 |

**成功响应**

```json
{
  "ok": true,
  "code": "msg.published",
  "message": "message published",
  "data": {
    "targetNode": "n1-11223344556677889900aabbccddeeff",
    "messageId": "msg-abc123",
    "sessionKey": "nats:n1-localnodeid:n1-11223344556677889900aabbccddeeff"
  },
  "ts": 1710000000000
}
```

**失败响应**

```json
{
  "ok": false,
  "code": "msg.publish_failed",
  "message": "peer not found: n1-11223344556677889900aabbccddeeff",
  "data": {
    "targetNode": "n1-11223344556677889900aabbccddeeff"
  },
  "ts": 1710000000000
}
```

---

## GET /v1/messages

获取最近收到的消息与执行事件（最多 100 条），仅保存在内存中，不是持久收件箱或完整事件回放。消费者用 requestId/runId/seq 关联输出；run.completed 不重复附带全文，不能把终态正文再次追加到 delta。

**响应**

```json
{
  "ok": true,
  "code": "msg.recent",
  "message": "recent messages fetched",
  "data": {
    "items": [
      {
        "id": "msg-abc123",
        "type": "chat.message",
        "from": "n1-11223344556677889900aabbccddeeff",
        "to": "n1-localnodeid",
        "payload": {"text": "报告已完成"},
        "sessionKey": "nats:n1-localnodeid:n1-11223344556677889900aabbccddeeff",
        "ts": 1710000000000,
        "expiresAt": 1710000600000,
        "sig": "base64-signature",
        "metadata": {},
        "protocolVersion": "v2"
      }
    ]
  },
  "ts": 1710000000000
}
```

`data.items[]` 字段说明（MessageEnvelope 结构）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 消息唯一 ID |
| `type` | string | 消息类型 |
| `agentId` | string | 目标节点本地智能体 ID，可能为空 |
| `from` | string | 发送方节点 ID |
| `to` | string | 接收方节点 ID |
| `payload` | object | 结构化正文或执行事件的 text/channel/code/stage/indeterminate/adapterRunId |
| `sessionKey` | string | 会话标识 |
| `ts` | int64 | 消息时间戳，Unix 毫秒 |
| `expiresAt` | int64 | 消息失效时间 |
| `requestId` | string | 执行事件的原请求 ID |
| `runId` | string | 节点生成的执行 ID |
| `seq` | uint64 | 单次执行事件序号，从 1 开始 |
| `sig` | string | 消息签名（Base64） |
| `metadata` | object | 附加元数据 |
| `protocolVersion` | string | 协议版本号 |

执行类型为 `run.started`、`run.output.delta`、`run.completed`、`run.failed`、`run.accepted`。accepted 是异步接纳；failed 中的 indeterminate=true 表示可能已产生副作用，不能自动重跑。公开事件不自动重新投递给 LLM。


---

## POST /v1/auth/challenge

向目标节点发起 challenge-response 认证握手。

**请求**

```json
{
  "targetNode": "n1-11223344556677889900aabbccddeeff"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `targetNode` | string | 是 | 目标节点 ID |

**成功响应**

```json
{
  "ok": true,
  "code": "auth.challenge_accepted",
  "message": "challenge completed",
  "data": {
    "targetNode": "n1-11223344556677889900aabbccddeeff",
    "status": "authenticated"
  },
  "ts": 1710000000000
}
```

**失败响应**

```json
{
  "ok": false,
  "code": "auth.challenge_failed",
  "message": "peer not found: n1-11223344556677889900aabbccddeeff",
  "data": {
    "targetNode": "n1-11223344556677889900aabbccddeeff"
  },
  "ts": 1710000000000
}
```

---

## GET /v1/trust/pending

获取待处理的信任请求列表。

**响应**

```json
{
  "ok": true,
  "code": "trust.pending",
  "message": "pending trust requests fetched",
  "data": {
    "items": [
      {
        "requestId": "req-xyz789",
        "from": "n1-11223344556677889900aabbccddeeff",
        "to": "n1-localnodeid",
        "direction": "incoming",
        "reason": "需要协作完成任务",
        "receivedAtMs": 1710000000000
      }
    ]
  },
  "ts": 1710000000000
}
```

`data.items[]` 字段说明（TrustPendingState 结构）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `requestId` | string | 请求唯一 ID |
| `from` | string | 请求发起方节点 ID |
| `to` | string | 请求接收方节点 ID |
| `direction` | string | 方向：`incoming`（收到的）或 `outgoing`（发出的） |
| `reason` | string | 请求理由 |
| `receivedAtMs` | int64 | 请求接收时间，Unix 毫秒 |

---

## POST /v1/trust/request

向目标节点发起信任请求。

**请求**

```json
{
  "targetNode": "n1-11223344556677889900aabbccddeeff",
  "reason": "需要协作完成任务",
  "capabilities": ["chat", "tools"]
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `targetNode` | string | 是 | 目标节点 ID |
| `reason` | string | 否 | 请求理由 |
| `capabilities` | string[] | 否 | 请求的能力列表 |

**成功响应**

```json
{
  "ok": true,
  "code": "trust.requested",
  "message": "trust request sent",
  "data": {
    "targetNode": "n1-11223344556677889900aabbccddeeff",
    "requestId": "req-xyz789"
  },
  "ts": 1710000000000
}
```

**失败响应**

```json
{
  "ok": false,
  "code": "trust.request_failed",
  "message": "peer not found: n1-11223344556677889900aabbccddeeff",
  "data": {
    "targetNode": "n1-11223344556677889900aabbccddeeff"
  },
  "ts": 1710000000000
}
```

---

## POST /v1/trust/approve

批准一个信任请求。

**请求**

```json
{
  "requestId": "req-xyz789",
  "reason": "已确认身份"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `requestId` | string | 是 | 信任请求 ID |
| `reason` | string | 否 | 批准理由 |

**成功响应**

```json
{
  "ok": true,
  "code": "trust.responded",
  "message": "trust decision sent",
  "data": {
    "requestId": "req-xyz789",
    "decision": "approve"
  },
  "ts": 1710000000000
}
```

---

## POST /v1/trust/reject

拒绝一个信任请求。

**请求**

```json
{
  "requestId": "req-xyz789",
  "reason": "未知节点"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `requestId` | string | 是 | 信任请求 ID |
| `reason` | string | 否 | 拒绝理由 |

**成功响应**

```json
{
  "ok": true,
  "code": "trust.responded",
  "message": "trust decision sent",
  "data": {
    "requestId": "req-xyz789",
    "decision": "reject"
  },
  "ts": 1710000000000
}
```

---

## POST /v1/trust/revoke

撤销对目标节点的信任。

**请求**

```json
{
  "targetNode": "n1-11223344556677889900aabbccddeeff",
  "reason": "节点行为异常"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `targetNode` | string | 是 | 目标节点 ID |
| `reason` | string | 否 | 撤销理由 |

**成功响应**

```json
{
  "ok": true,
  "code": "trust.revoked",
  "message": "trust revoked",
  "data": {
    "targetNode": "n1-11223344556677889900aabbccddeeff"
  },
  "ts": 1710000000000
}
```

---

## POST /v1/transfer/send

向目标节点发送文件。该接口要求本地 NATS 连接可用且服务端启用了 JetStream。

**请求**

```json
{
  "targetNode": "n1-11223344556677889900aabbccddeeff",
  "filePath": "/tmp/report.pdf",
  "mimeType": "application/pdf",
  "metadata": { "taskId": "task-001", "todoId": "todo-042" }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `targetNode` | string | 是 | 目标节点 ID |
| `filePath` | string | 是 | 本地待发送文件路径 |
| `mimeType` | string | 否 | 文件 MIME 类型 |
| `metadata` | object | 否 | 业务扩展元数据（如 `taskId`、`todoId` 等），随文件传递到接收方 |

**成功响应**

```json
{
  "ok": true,
  "code": "transfer.sent",
  "message": "file transfer initiated",
  "data": {
    "transferId": "tf_abc123",
    "bucket": "clawsynapse-transfer-n1-11223344556677889900aabbccddeeff",
    "size": 24576,
    "checksum": "SHA-256=abcdef..."
  },
  "ts": 1710000000000
}
```

**常见失败响应**

JetStream 不可用：

```json
{
  "ok": false,
  "code": "transfer.disabled",
  "message": "transfer service not available (jetstream required)",
  "ts": 1710000000000
}
```

发送失败：

```json
{
  "ok": false,
  "code": "transfer.send_failed",
  "message": "peer is not authenticated",
  "data": {
    "targetNode": "n1-11223344556677889900aabbccddeeff"
  },
  "ts": 1710000000000
}
```

---

## GET /v1/transfers

获取当前进程内维护的传输记录列表，包括发送和接收的文件传输。

**响应**

```json
{
  "ok": true,
  "code": "transfer.list",
  "message": "transfers fetched",
  "data": {
    "items": [
      {
        "transferId": "tf_abc123",
        "direction": "outbound",
        "peerNode": "n1-11223344556677889900aabbccddeeff",
        "fileName": "report.pdf",
        "fileSize": 24576,
        "mimeType": "application/pdf",
        "checksum": "SHA-256=abcdef...",
        "status": "completed",
        "createdAt": 1710000000000,
        "completedAt": 1710000001000
      }
    ]
  },
  "ts": 1710000000000
}
```

`data.items[]` 字段说明（TransferInfo 结构）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `transferId` | string | 传输 ID |
| `direction` | string | 方向：`outbound` 或 `inbound` |
| `peerNode` | string | 对端节点 ID |
| `fileName` | string | 文件名 |
| `fileSize` | int64 | 文件大小，单位字节 |
| `mimeType` | string | 文件 MIME 类型 |
| `checksum` | string | 对象校验摘要 |
| `status` | string | 当前状态，当前实现通常为 `completed` |
| `localPath` | string | 本地接收文件路径，仅入站文件通常会有该字段 |
| `metadata` | object | 业务扩展元数据，发送时附带的自定义键值对 |
| `createdAt` | int64 | 记录创建时间，Unix 毫秒 |
| `completedAt` | int64 | 完成时间，Unix 毫秒 |

JetStream 不可用时，列表接口仍然可用；只有当传输服务未初始化时才会返回：

```json
{
  "ok": false,
  "code": "transfer.disabled",
  "message": "transfer service not available",
  "ts": 1710000000000
}
```

---

## GET /v1/transfer/{transferId}

获取单个传输详情。

路径参数：

| 参数 | 类型 | 说明 |
|------|------|------|
| `transferId` | string | 传输 ID |

**成功响应**

```json
{
  "ok": true,
  "code": "transfer.detail",
  "message": "transfer fetched",
  "data": {
    "transfer": {
      "transferId": "tf_abc123",
      "direction": "inbound",
      "peerNode": "n1-11223344556677889900aabbccddeeff",
      "fileName": "report.pdf",
      "fileSize": 24576,
      "mimeType": "application/pdf",
      "checksum": "SHA-256=abcdef...",
      "status": "completed",
      "localPath": "/Users/demo/.clawsynapse/transfers/tf_abc123-report.pdf",
      "metadata": { "taskId": "task-001" },
      "createdAt": 1710000000000,
      "completedAt": 1710000001000
    }
  },
  "ts": 1710000000000
}
```

未找到时：

```json
{
  "ok": false,
  "code": "transfer.not_found",
  "message": "transfer not found",
  "ts": 1710000000000
}
```

---

## DELETE /v1/transfer/{transferId}

删除传输记录；如果记录关联 JetStream Object Store，也会尝试删除远端对象。当前实现即使对象删除失败，也会继续清理本地记录。

路径参数：

| 参数 | 类型 | 说明 |
|------|------|------|
| `transferId` | string | 传输 ID |

**成功响应**

```json
{
  "ok": true,
  "code": "transfer.deleted",
  "message": "transfer deleted",
  "data": {
    "transferId": "tf_abc123"
  },
  "ts": 1710000000000
}
```

JetStream 不可用时：

```json
{
  "ok": false,
  "code": "transfer.disabled",
  "message": "transfer service not available",
  "ts": 1710000000000
}
```

删除失败时：

```json
{
  "ok": false,
  "code": "transfer.delete_failed",
  "message": "transfer not found",
  "ts": 1710000000000
}
```

---

## Go 客户端

`internal/api` 包提供了 `Client` 结构，可在 Go 代码中直接调用本地 API：

```go
c := api.NewClient("127.0.0.1:18080", 5*time.Second)

// GET 请求
result, err := c.Get(ctx, "/v1/peers")

// POST 请求
result, err := c.Post(ctx, "/v1/publish", map[string]any{
    "targetNode": "n1-11223344556677889900aabbccddeeff",
    "message":    "hello",
})

// PUT 请求（例如更新本地备注）
result, err = c.Put(ctx, "/v1/peers/n1-11223344556677889900aabbccddeeff/attrs", map[string]any{
    "alias":  "现场网关",
    "labels": map[string]any{"floor": "3"},
})

// DELETE 请求
result, err = c.Delete(ctx, "/v1/transfer/tf_abc123")
```

`Client` 自动处理 JSON 序列化/反序列化，返回值统一为 `types.APIResult`。当 HTTP 状态码 >= 400 时，返回 error 且 `result.OK` 为 `false`。

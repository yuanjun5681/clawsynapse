---
summary: "ClawSynapse CLI：本地命令行入口与命令说明"
title: "ClawSynapse CLI"
---

# ClawSynapse CLI

最后更新：2026-03-12

`clawsynapse` 是 ClawSynapse 的本地命令行入口。它不直接连接 NATS，而是通过 `clawsynapsed` 暴露的本地 HTTP API 执行查询、消息发送与信任管理操作。

## 使用方式

在仓库根目录直接运行：

```bash
go run ./cmd/clawsynapse <command>
```

如果已经构建成二进制：

```bash
clawsynapse <command>
```

默认本地 API 地址为 `127.0.0.1:18080`。

## 全局参数

- `--api-addr`：指定本地 API 地址，默认 `127.0.0.1:18080`
- `--timeout`：指定请求超时，默认 `5s`
- `--json`：直接输出 API 返回的 JSON，适合脚本调用

示例：

```bash
go run ./cmd/clawsynapse --api-addr 127.0.0.1:18080 --timeout 10s --json health
```

## 当前命令

### Health

检查本地 daemon 与 NATS 连接状态：

```bash
go run ./cmd/clawsynapse health
go run ./cmd/clawsynapse --json health
```

对应 API：

```http
GET /v1/health
```

### Peers

查看当前已发现 peer：

```bash
go run ./cmd/clawsynapse peers
```

对应 API：

```http
GET /v1/peers
```

### Messages

查看最近消息记录：

```bash
go run ./cmd/clawsynapse messages
```

对应 API：

```http
GET /v1/messages
```

### Publish

向目标节点发送消息：

```bash
go run ./cmd/clawsynapse publish \
  --target node-beta \
  --message "请汇总最新报告"
```

带会话键与元数据：

```bash
go run ./cmd/clawsynapse publish \
  --target node-beta \
  --message "请汇总最新报告" \
  --session-key nats:node-alpha:node-beta \
  --metadata priority=high \
  --metadata source=cli
```

对应 API：

```http
POST /v1/publish
```

普通输出会单独显示 `targetNode` 和 `messageId`；如果需要完整结构，使用 `--json`。

### Request

向目标节点发送请求并等待 reply：

```bash
go run ./cmd/clawsynapse request \
  --target node-beta \
  --message "当前状态如何？"
```

带会话键、元数据与自定义超时：

```bash
go run ./cmd/clawsynapse request \
  --target node-beta \
  --message "请返回最新处理结果" \
  --session-key nats:node-alpha:node-beta \
  --metadata priority=high \
  --timeout-ms 15000
```

对应 API：

```http
POST /v1/request
```

普通输出会单独显示 `reply`、`runId`、`from` 和 `requestId`；如果需要完整结构，使用 `--json`。

### Auth Challenge

对目标节点发起 challenge：

```bash
go run ./cmd/clawsynapse auth challenge --target node-beta
```

对应 API：

```http
POST /v1/auth/challenge
```

### Trust

发起信任请求：

```bash
go run ./cmd/clawsynapse trust request \
  --target node-beta \
  --reason "需要建立跨节点协作" \
  --capability chat \
  --capability tools
```

查看待处理请求：

```bash
go run ./cmd/clawsynapse trust pending
```

批准请求：

```bash
go run ./cmd/clawsynapse trust approve \
  --request-id req_123 \
  --reason "已人工确认"
```

拒绝请求：

```bash
go run ./cmd/clawsynapse trust reject \
  --request-id req_123 \
  --reason "来源不明"
```

撤销信任：

```bash
go run ./cmd/clawsynapse trust revoke \
  --target node-beta \
  --reason "密钥已轮换"
```

对应 API：

```http
GET  /v1/trust/pending
POST /v1/trust/request
POST /v1/trust/approve
POST /v1/trust/reject
POST /v1/trust/revoke
```

`trust request`、`trust approve`、`trust reject`、`trust revoke` 的普通输出会单独显示关键字段，例如 `targetNode`、`requestId` 和 `decision`；如果需要完整结构，使用 `--json`。

## 命令与 API 对照

| CLI | API |
|-----|-----|
| `clawsynapse health` | `GET /v1/health` |
| `clawsynapse peers` | `GET /v1/peers` |
| `clawsynapse messages` | `GET /v1/messages` |
| `clawsynapse publish` | `POST /v1/publish` |
| `clawsynapse request` | `POST /v1/request` |
| `clawsynapse auth challenge` | `POST /v1/auth/challenge` |
| `clawsynapse trust pending` | `GET /v1/trust/pending` |
| `clawsynapse trust request` | `POST /v1/trust/request` |
| `clawsynapse trust approve` | `POST /v1/trust/approve` |
| `clawsynapse trust reject` | `POST /v1/trust/reject` |
| `clawsynapse trust revoke` | `POST /v1/trust/revoke` |

## 当前边界

当前 CLI 只覆盖已经在 `clawsynapsed` 中实现的本地 API。

尚未纳入 CLI 的能力包括：

- 直接指定任意 `subject` 的发布
- 更完整的管理命令集合

这些能力以实际后端实现为准，补齐后再扩展 CLI 命令集。

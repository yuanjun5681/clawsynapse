---
summary: "ClawSynapse CLI：本地命令行入口与命令说明"
title: "ClawSynapse CLI"
---

# ClawSynapse CLI

最后更新：2026-10-20

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

所有 API 路由已启用 Bearer 认证。daemon 首次启动生成 `DataDir/api.token`（0600）；CLI 自动读取本地配置中的 token 路径，或通过 `API_TOKEN_PATH` 指定。缺失文件会明确失败，不会匿名降级。远端 API 必须使用受信任证书的 HTTPS；本地客户端不跟随重定向。

当前支持的顶层命令：

- `version`
- `init`
- `service status|start|stop|restart`
- `dashboard`
- `logs`
- `health`
- `peers`
- `peer alias|describe|label|unlabel`
- `messages`
- `publish`
- `auth challenge`
- `trust pending|request|approve|reject|revoke`
- `transfer send|get|delete|list`
- `transfers`

## 全局参数

- `--api-addr`：指定本地 API 地址，默认 `127.0.0.1:18080`
- `--timeout`：指定请求超时，默认 `5s`
- `--json`：直接输出 API 返回的 JSON，适合脚本调用

示例：

```bash
go run ./cmd/clawsynapse --api-addr 127.0.0.1:18080 --timeout 10s --json health
```

## 当前命令

### Version

打印当前 CLI 二进制版本：

```bash
go run ./cmd/clawsynapse version
```

### Init

交互式生成或更新 `~/.clawsynapse/config.yaml`：

```bash
go run ./cmd/clawsynapse init
```

非交互覆盖写入：

```bash
go run ./cmd/clawsynapse init \
  --overwrite \
  --nats-servers nats://127.0.0.1:4222 \
  --agent-adapter openclaw \
  --trust-auto-approve=false
```

常见用途：

- 首次安装后补全 daemon 配置
- 修改 NATS 地址或 adapter
- 生成标准化的 `~/.clawsynapse/config.yaml`

说明：

- `nodeId` 不在配置中手工设置
- daemon 会从本地 Ed25519 公钥自动派生 `did:key` 和 `nodeId`
- `--trust-auto-approve=true` 会自动批准签名校验通过的 inbound trust request，仅建议在受控网络使用

配置写入后，使用 service 子命令应用变更：

```bash
go run ./cmd/clawsynapse service restart
```

### Service

管理本机上的 `clawsynapsed` 服务：

```bash
go run ./cmd/clawsynapse service status
go run ./cmd/clawsynapse service restart
go run ./cmd/clawsynapse service stop
go run ./cmd/clawsynapse service start
```

平台行为：

- Linux: 调用 `sudo systemctl`
- macOS: 调用 `launchctl`

### Dashboard

启动一个终端监控界面（Config 视图支持编辑配置）：

```bash
go run ./cmd/clawsynapse dashboard
```

当前提供五个视图：

- `Overview`：NATS 连接、消息计数、peer 数
- `Peers`：已发现节点列表，每行展示可读名称（本地别名优先于远端 displayName）与身份短指纹（宽度不足时指纹、nodeId 列逐级省略）；选中节点可查看 displayName/alias/description、远端与本地 labels 及完整指纹
- `Messages`：最近消息快照
- `Logs`：最近服务日志
- `Config`：查看并编辑配置，包括 `displayName` 与 `labels`（labels 以 JSON 对象输入，例如 `{"env":"prod","caps":["chat","tools"]}`；非法 JSON 时中止保存并报错）

快捷键：

- `q`：退出
- `tab` / `shift+tab` / `left` / `right`：切换视图
- `1` / `2` / `3` / `4` / `5`：直接跳到对应视图
- `r`：立即刷新
- Peers 视图内 `/`：输入筛选表达式；`key=value` 为 label 字符串精确匹配，`key~=value` 为 label 数组成员匹配，其他输入按名称/别名/nodeId 子串匹配；`Esc` 清除筛选
- Config 视图内 `enter` 编辑字段，`S` 保存（保存后需重启 daemon 生效）

### Logs

查看最近服务日志：

```bash
go run ./cmd/clawsynapse logs
go run ./cmd/clawsynapse logs --lines 200
go run ./cmd/clawsynapse logs --follow
```

平台行为：

- Linux: 优先读取 `~/.clawsynapse/log/clawsynapsed.log`，不存在时回退到 `journalctl -u clawsynapsed.service`
- macOS: 优先读取 `~/.clawsynapse/log/clawsynapsed.log`，兼容回退到旧的 `clawsynapsed.stdout.log` 和 `clawsynapsed.stderr.log`

### Health

检查本地 daemon、NATS 连接状态以及当前 Agent Adapter 健康状态：

```bash
go run ./cmd/clawsynapse health
go run ./cmd/clawsynapse --json health
```

对应 API：

```http
GET /v1/health
```

普通输出包含 `nodeId`、`displayName`、`labels`（标量 `k=v`、集合 `k=[a,b]`）、版本、`trustMode`、peer 数，以及 NATS 与 adapter 状态；完整结构请用 `--json`。

### Peers

查看当前已发现 peer：

```bash
go run ./cmd/clawsynapse peers
go run ./cmd/clawsynapse peers --name gateway
go run ./cmd/clawsynapse peers --label env=production
go run ./cmd/clawsynapse peers --label-has caps=chat
```

表格列：`NAME`（本地别名优先，其次远端 displayName，否则 `-`）、`NODE`（身份短指纹）、`AUTH`、`TRUST`、`LABELS`（标量渲染为 `k=v`，集合渲染为 `k=[a,b]`）。

筛选参数：

- `--label key=value`：label 字符串精确匹配，可重复
- `--label-has key=value`：label 数组成员匹配，可重复；与 `--label` 语义不同，不做逗号拆分
- `--name substr`：对 displayName/本地别名做大小写不敏感的子串匹配

对应 API：

```http
GET /v1/peers?label=env=production&labelHas=caps=chat&name=gateway
```

### Peer 本地备注

维护本节点上针对某个 peer 的备注（仅本地可见，不影响身份与信任，持久化于 `peer-attrs.json`）：

```bash
# 设置/清除别名（不带 alias 参数即清除）
go run ./cmd/clawsynapse peer alias <nodeID> 现场网关
go run ./cmd/clawsynapse peer alias <nodeID>

# 设置/清除描述
go run ./cmd/clawsynapse peer describe <nodeID> 上海机房

# 设置本地 label：一个值为标量，多个值为集合
go run ./cmd/clawsynapse peer label <nodeID> floor 3
go run ./cmd/clawsynapse peer label <nodeID> zone a b

# 删除某个本地 label 键
go run ./cmd/clawsynapse peer unlabel <nodeID> floor
```

对应 API：

```http
GET /v1/peers/{nodeID}/attrs
PUT /v1/peers/{nodeID}/attrs
DELETE /v1/peers/{nodeID}/attrs
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
  --target <peer-node-id> \
  --message "请汇总最新报告"
```

指定消息类型（默认 `chat.message`）：

```bash
go run ./cmd/clawsynapse publish \
  --target <peer-node-id> \
  --type task.assign \
  --message "请处理数据清洗任务"
```

带会话键与元数据：

```bash
go run ./cmd/clawsynapse publish \
  --target <peer-node-id> \
  --message "请汇总最新报告" \
  --session-key nats:<local-node-id>:<peer-node-id> \
  --metadata priority=high \
  --metadata source=cli
```

对应 API：

```http
POST /v1/publish
```

普通输出会单独显示 `targetNode`、`messageId` 和 `sessionKey`；如果需要完整结构，使用 `--json`。

publish 成功不是 Agent 完成。文本会映射为 v2 `payload.text`，结构化任务可通过本地 HTTP API 的 `payload` 提交；`messages --json` 可查看关联的 `run.*` 事件。当前没有持久执行查询、事件补取或 publish 等待完成选项。

可通过 `--agent <id>` 请求命名 Agent，但接收端 OpenClaw/OpenCode 必须先在 `agentAllowedIds` 中授权该名称；默认拒绝命名路由。Codex 不支持此参数，明确返回执行失败，而非静默使用默认 Agent。

### Auth Challenge

对目标节点发起 challenge：

```bash
go run ./cmd/clawsynapse auth challenge --target n1-11223344556677889900aabbccddeeff
```

对应 API：

```http
POST /v1/auth/challenge
```

### Trust

发起信任请求：

```bash
go run ./cmd/clawsynapse trust request \
  --target n1-11223344556677889900aabbccddeeff \
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
  --target n1-11223344556677889900aabbccddeeff \
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

### Transfer

通过本地 daemon 调用文件传输接口。文件传输依赖服务端启用 JetStream。

发送文件：

```bash
go run ./cmd/clawsynapse transfer send \
  --target n1-11223344556677889900aabbccddeeff \
  --file /tmp/report.pdf
```

带 MIME 类型发送：

```bash
go run ./cmd/clawsynapse transfer send \
  --target n1-11223344556677889900aabbccddeeff \
  --file /tmp/report.pdf \
  --mime-type application/pdf
```

带业务元数据发送：

```bash
go run ./cmd/clawsynapse transfer send \
  --target n1-11223344556677889900aabbccddeeff \
  --file /tmp/report.pdf \
  --metadata taskId=task-001 \
  --metadata todoId=todo-042
```

获取单个传输详情：

```bash
go run ./cmd/clawsynapse transfer get --id tf_abc123
```

删除传输记录：

```bash
go run ./cmd/clawsynapse transfer delete --id tf_abc123
```

查看传输列表：

```bash
go run ./cmd/clawsynapse transfer list
go run ./cmd/clawsynapse transfers
```

对应 API：

```http
POST   /v1/transfer/send
GET    /v1/transfer/{transferId}
DELETE /v1/transfer/{transferId}
GET    /v1/transfers
```

普通输出行为：

- `transfer send` 会单独输出 `transferId`、`bucket`、`size`、`checksum`
- `transfer get` 会输出 `data.transfer` 对象的格式化 JSON
- `transfer list` 和 `transfers` 会先输出 `items: <N>`，再输出列表 JSON
- `transfer delete` 会单独输出 `transferId`

常见失败场景：

- JetStream 不可用时，daemon 会返回 `transfer.disabled`
- 目标节点未认证、未受信任、文件不存在或超出大小限制时，daemon 会返回 `transfer.send_failed`

## 命令与 API 对照

| CLI | API |
|-----|-----|
| `clawsynapse health` | `GET /v1/health` |
| `clawsynapse peers` | `GET /v1/peers` |
| `clawsynapse peer alias <nodeID> [alias]` | `PUT /v1/peers/{nodeID}/attrs` |
| `clawsynapse peer describe <nodeID> [text]` | `PUT /v1/peers/{nodeID}/attrs` |
| `clawsynapse peer label <nodeID> <key> <value> [value...]` | `PUT /v1/peers/{nodeID}/attrs` |
| `clawsynapse peer unlabel <nodeID> <key>` | `PUT /v1/peers/{nodeID}/attrs` |
| `clawsynapse messages` | `GET /v1/messages` |
| `clawsynapse publish` | `POST /v1/publish` |
| `clawsynapse auth challenge` | `POST /v1/auth/challenge` |
| `clawsynapse trust pending` | `GET /v1/trust/pending` |
| `clawsynapse trust request` | `POST /v1/trust/request` |
| `clawsynapse trust approve` | `POST /v1/trust/approve` |
| `clawsynapse trust reject` | `POST /v1/trust/reject` |
| `clawsynapse trust revoke` | `POST /v1/trust/revoke` |
| `clawsynapse transfer send` | `POST /v1/transfer/send` |
| `clawsynapse transfer get --id <transferId>` | `GET /v1/transfer/{transferId}` |
| `clawsynapse transfer delete --id <transferId>` | `DELETE /v1/transfer/{transferId}` |
| `clawsynapse transfer list` | `GET /v1/transfers` |
| `clawsynapse transfers` | `GET /v1/transfers` |

## 当前边界

当前 CLI 只覆盖已经在 `clawsynapsed` 中实现的本地 API。

尚未纳入 CLI 的能力包括：

- 直接指定任意 `subject` 的发布
- 守护进程启动参数管理
- 守护进程配置编辑的 CLI 命令（`dashboard` 的 Config 视图可直接编辑配置；CLI 命令本身没有独立入口）
- 更细粒度的批量传输管理能力

这些能力以实际后端实现为准，补齐后再扩展 CLI 命令集。

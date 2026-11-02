---
summary: "ClawSynapse CLI：daemon 初始化、显式 Bot 管理、迁移与业务操作"
title: "ClawSynapse CLI"
---

# ClawSynapse CLI

`clawsynapse` 通过本地 daemon HTTP API 管理 Bot 和业务；只有 init、迁移、离线 export/import/convert 与服务/日志管理直接操作本机。仓库内可用 `go run ./cmd/clawsynapse` 替换下列 `clawsynapse`。

## 全局参数与凭据

全局参数放在命令前：

```bash
clawsynapse --api-addr 127.0.0.1:18080 --json bots
clawsynapse --bot "$BOT_A" --timeout 10s peers
```

- `--api-addr`：仅限 loopback 的 daemon endpoint，默认从本地 daemon 配置读取（默认 127.0.0.1:18080）。远端地址即使使用 HTTPS 也拒绝。
- `--token-file`：可选 Bot scoped 凭据文件；默认不读取或发送 token，本机管理无需凭据。显式提供错误 token 会失败，不会匿名回退。
- `--bot`：业务命令必填，本地完整 Bot ID 或无歧义前缀；即便只有一个 Bot 也不默认选择。前缀解析调用 bots 列表；显式使用 Bot scoped token 时无此权限，必须使用完整 `n1-` + 32 位小写十六进制 ID。
- `--timeout`：HTTP 请求超时，默认 5s；不是远端模型执行超时。
- `--json`：输出 APIResult JSON。客户端不跟随重定向。

本机任何进程均可不带 token 管理所有 Bot。`bots/{botId}/credentials/api.token` 是可选的细粒度业务凭据；显式使用它时仅授权该 Bot 的业务和身份查询，不能执行 bots、bot config、生命周期或宿主文件导入，但省略 token 即恢复完整本机管理能力，因此不提供本机进程隔离。不要将 API 代理/转发给不可信调用方。`--bot` 选择本地发起者，`--target` 是远端 Bot ID，二者不能混用。跨设备通信通过 NATS，Bot 间签名、认证与信任不变；不支持远程 daemon 管理。HTTP API 没有 v1 别名或默认 Bot，详见 [api.md](api.md)。

## 初始化 daemon 与两个 Bot

```bash
clawsynapse init --nats-servers nats://127.0.0.1:4222
clawsynapsed --config "$HOME/.clawsynapse/config.json"
# 另一个终端：无需 token，直接创建两份独立配置和身份
clawsynapse --json bot create --name assistant-a --file /absolute/bot-a.json
clawsynapse --json bot create --name assistant-b --file /absolute/bot-b.json
clawsynapse --json bots
```

`init` 只创建 schemaVersion 3 的 daemon `config.json`，不创建默认 Bot、不迁移旧目录、不配置 Role 或 Harness。终端可交互输入，非交互使用参数：`--config FILE`、`--data-dir ROOT`、`--nats-servers URLS`、`--local-api-addr ADDR`、`--log-level debug|info|warn|error`、`--log-format json|text`、`--overwrite`。已有文件非交互覆盖必须显式 `--overwrite`。不支持 init 的 `--agent-adapter`、trust、gateway 或身份参数。daemon 配置修改后重启 daemon。

Bot 创建文件必须显式提供 `role`；Agent 还必须显式提供 `harness.adapter`，其他适用字段按角色填充默认值。例如 bot-a.json：

```json
{"displayName":"assistant-a","role":"agent","harness":{"adapter":"opencode","model":"zai-coding-plan/glm-5.3-flash","timeout":"10m"}}
```

bot-b.json 使用另一 displayName/model。模型必须在部署机器上真实可用，此示例不是模型验收声明。创建生成独立密钥和 scoped token，返回 `data.bot.botId`；保存为 BOT_A/B。Bot 初始 stopped：

```bash
clawsynapse --bot "$BOT_A" bot start
clawsynapse --bot "$BOT_B" bot start
clawsynapse --bot "$BOT_A" bot get
clawsynapse --bot "$BOT_B" health
```

start/stop 接纳为 HTTP 202；用 bot get 轮询到 running/stopped/failed，不把 202 当成 ready。`daemon` 查询 daemon 汇总健康；`health` 查询所选 Bot 的 self/nats/adapter，不代表真实模型任务已成功。

## Bot 配置与生命周期

```bash
clawsynapse bots
clawsynapse --bot "$BOT_A" --json bot get
clawsynapse --bot "$BOT_A" --json bot config
clawsynapse --bot "$BOT_A" bot stop
# 等待 bot get 显示 stopped，再将 GET 的 data.config 保存/编辑为完整 JSON
clawsynapse --bot "$BOT_A" bot config --file /absolute/bot-a-updated.json
clawsynapse --bot "$BOT_A" bot start
clawsynapse --bot "$BOT_B" bot stop
# 等待 stopped
clawsynapse --bot "$BOT_B" bot archive
clawsynapse --bot "$BOT_B" bot restore
```

无文件创建使用 `bot create --name NAME --role console`，或 `bot create --name NAME --role agent --adapter ADAPTER`；不从 adapter 推断 Role，也不为 Agent 猜测 adapter。文件创建使用 `bot create --file FILE [--name NAME]`，Role/adapter 必须写在文件中，不能同时传 `--role`/`--adapter`；`--name` 覆盖显示名。`device` 尚不可创建。

Console 文件示例为 `{"displayName":"inbox","role":"console"}`。Console 必须省略 `harness`（`null`、`{}` 也不允许）、`maxConcurrent` 和 `maxQueued`；保留公共信任、标签与文件配额。Console health 显示 `harness: not applicable`，不是执行故障。只有 Agent 配置 Harness adapter/model/timeout 和执行上限；OpenClaw/Webhook 必须有明确、互不共享的 harness.instance。所有字段见 [operations.md](operations.md)。

`bot config --file FILE` 不是 patch：提交包含原 Role 的完整 BotConfig 和当前 revision，成功递增；陈旧 revision 返回 409。普通更新不能切换 Role。Agent Harness 变化自动生成 bindingRevision，切换工作区及会话，不复活旧产品 session；纯 schema 迁移保留原 Agent bindingRevision。archive 保留数据，restore 回到 stopped，需显式 start。

## 旧数据显式迁移与私有备份

先停止 daemon（包括手工进程），保留旧配置和目录。不要先对旧目录运行 init --overwrite。版本 2 多 Bot 布局使用整体离线升级；旧单 Bot 配置使用显式 legacy 入口：

```bash
clawsynapse service stop
# schemaVersion 2 多 Bot 布局 → 3（包括 stopped/archived Bot）
clawsynapse migrate --data-dir "$HOME/.clawsynapse"
# 旧单 Bot YAML → schemaVersion 3；两种入口按实际源布局二选一
clawsynapse migrate --legacy-config "$HOME/.clawsynapse/config.yaml" --data-dir "$HOME/.clawsynapse"
# 输出保留原身份的完整 Bot ID；迁移后初始 stopped
clawsynapsed --config "$HOME/.clawsynapse/config.json"
clawsynapse bots
clawsynapse --bot "$MIGRATED_BOT" bot start
```

旧单 Bot 迁移的 `--data-dir` 可为原目录或单独空目录；源和目标不可嵌套。版本 2 布局升级必须针对整个原 DataDir。迁移持有独占锁，保留原始数据与备份；不在线猜测、合并目录或重建身份。Agent 保留 bindingRevision、原生 Session 和工作目录；Console 删除旧 Harness/执行预算配置，但保留历史文件路径与授权引用。原地切换通过暂存、备份和持久 marker 提交，不能将多次重命名视为一次原子操作。未完成迁移拒绝正常启动，使用 `clawsynapse migrate --recover --data-dir ROOT` 显式恢复；恢复可重复执行，不清空 outbox、pending work 或 cursor。新布局已有写入后，不直接用旧备份覆盖新历史。

停止目标 daemon 后可离线导出/导入 Bot：

```bash
clawsynapse --bot "$BOT_A" bot export --data-dir "$HOME/.clawsynapse" --file /private/backups/bot-a
clawsynapse bot import --data-dir /private/other-daemon --file /private/backups/bot-a
```

`--file` 是私有 bundle **目录**，不是 JSON 配置或压缩包；export 必须用完整 --bot ID，import 从 bundle 获取身份。导出包含身份私钥、凭据、配置、持久状态和引用内容，按秘密管理。离线操作不使用 HTTP token 绕过目录锁；源/目标 daemon 必须停止。导入保留身份，不是复制成新的 Bot，不要同时启动同一身份的两份副本。

普通 import 只接受 schemaVersion 3 bundle。旧版本 2 bundle 先显式转换，保留原包；目标必须是独立新目录，未知或更高版本拒绝：

```bash
clawsynapse bot convert --file /private/backups/bot-a-v2 --output /private/backups/bot-a-v3
clawsynapse bot import --data-dir /private/other-daemon --file /private/backups/bot-a-v3
```

## 业务命令

下列全部显式选择本地 Bot：

```bash
clawsynapse --bot "$BOT_A" health
clawsynapse --bot "$BOT_A" peers --name gateway --label env=production --label-has caps=chat
clawsynapse --bot "$BOT_A" peer alias "$PEER" 现场网关
clawsynapse --bot "$BOT_A" peer describe "$PEER" 上海机房
clawsynapse --bot "$BOT_A" peer label "$PEER" zone a b
clawsynapse --bot "$BOT_A" peer unlabel "$PEER" zone
clawsynapse --bot "$BOT_A" auth challenge --target "$PEER"
clawsynapse --bot "$BOT_A" trust request --target "$PEER" --reason 协作 --capability chat
clawsynapse --bot "$BOT_A" trust pending
clawsynapse --bot "$BOT_A" trust approve --request-id "$REQUEST_ID" --reason 已确认
clawsynapse --bot "$BOT_A" trust reject --request-id "$REQUEST_ID" --reason 拒绝
clawsynapse --bot "$BOT_A" trust revoke --target "$PEER" --reason 撤销
clawsynapse --bot "$BOT_A" publish --target "$PEER" --type chat.message \
  --message "汇总报告" --session-key report-session --metadata priority=high
clawsynapse --bot "$BOT_A" --json messages
```

备注/认证/信任按本地 Bot 隔离，Bot A 的 alias 或信任不影响 B。peer alias/describe 不带值清除；label 一个值为标量，多个值为集合；unlabel 删除键。标量/数组筛选不互换，不以逗号拆分。labels 是自报属性，不是授权。聊天对象是 Bot，不是 Harness 内部 Agent 目录；`publish --agent ID` 只是受接收端 `harness.allowedAgentIds` 限制的可选内部路由，Codex 不支持命名路由。

CLI publish 是文本快捷入口，只支持 `--target`、`--type`、`--agent`、`--message`、`--session-key`、可重复 `--metadata key=value`。结构化 payload、expiresAt、稳定 idempotencyKey、请求查询和游标回放用 HTTP API；没有 publish 等待模型完成选项。发布成功只是持久接纳，messages 包含关联 run.* 事件；持久恢复边界见 [messaging.md](messaging.md)。

## 文件导入、发送、读取与删除引用

```bash
# 本机不带 token 从宿主机导入，只给 BOT_A 建立引用
clawsynapse --bot "$BOT_A" --json transfer import --file /absolute/host/report.pdf
# 使用返回 transferId；授权引用转发，无须获知 blob 路径
clawsynapse --bot "$BOT_A" transfer forward --id "$TRANSFER_ID" --target "$PEER"
# 或直接发送当前 Bot/绑定 workspace 内的文件
clawsynapse --bot "$BOT_A" transfer send --target "$PEER" --file report.pdf \
  --mime-type application/pdf --metadata taskId=task-1
clawsynapse --bot "$BOT_A" transfers
clawsynapse --bot "$BOT_A" transfer list
clawsynapse --bot "$BOT_A" transfer get --id "$TRANSFER_ID"
clawsynapse --bot "$BOT_A" transfer delete --id "$TRANSFER_ID"
```

transfer send 不能读取任意宿主路径/其他 Bot 文件。scoped token 可发送 workspace 文件、转发和读取自己的引用，但不能 transfer import。CLI 没有文件字节下载子命令；使用 HTTP：

```bash
curl --fail -H "Authorization: Bearer $(cat "$TOKEN_FILE")" \
  "http://127.0.0.1:18080/v2/bots/$BOT_A/transfer/$TRANSFER_ID/file" -o report.pdf
```

删除仅删除所选 Bot 的引用，不删除其他 Bot 的授权内容；共享无引用内容后续由 GC 回收。传输需要 JetStream 与对端认证/信任，配额失败不会旁路。

## 本机服务与监控

`version` 输出 CLI 版本，`clawsynapsed --version` 输出 daemon 版本。`service status|start|stop|restart` 管理整个 daemon（macOS launchd / Linux systemd），不是单 Bot 生命周期。`logs [--lines 200] [--follow]` 读取 daemon 日志，默认 `~/.clawsynapse/log/clawsynapsed.log`，兼容平台旧日志回退。

`dashboard` 提供终端监控视图；显式 `--bot` 选择业务对象，默认无需 token。`--token-file` 仅用于可选 Bot scoped 调用，使用时不会获得 catalog/配置管理权。实际验收已覆盖从运行 Bot A 切换到 stopped 的另一 Bot 时清除原消息/缓存；这不等于所有平台和视图穷举验收，真实模型与第三方 CLI 版本兼容仍按部署组合确认。

### Upgrade

`clawsynapse upgrade check [--json]` 查询安装状态和最新 release；`clawsynapse upgrade [--cli|--daemon|--all] [--version TAG] [--repo OWNER/REPO] [--yes]` 调用安装器升级选定组件。三个组件选择参数互斥，省略时根据已安装组件选择；版本默认 latest，默认要求交互确认，自动化才显式 --yes。它不代替旧布局停机迁移，也不应覆盖 Bot 配置/身份。

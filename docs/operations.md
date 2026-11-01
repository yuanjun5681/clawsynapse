---
summary: "ClawSynapse 运行与配置：Go 实现、目录结构、配置项与阶段规划"
title: "ClawSynapse Operations"
---

# ClawSynapse Operations

最后更新：2026-10-31

## Go 实现

Go 用于实现 ClawSynapse：

- 并发模型覆盖长期订阅、心跳、握手和超时控制
- 打包为单二进制守护进程
- 标准库覆盖网络、HTTP 与加密支持
- 部署为本地设备上的长期运行进程

## 依赖

- NATS：`github.com/nats-io/nats.go`
- WebSocket：`github.com/coder/websocket`
- 日志：`log/slog`
- 加密：标准库 `crypto/ed25519`、`crypto/sha256`、`crypto/rand`
- HTTP：标准库 `net/http`

## 目录结构

```text
cmd/clawsynapsed/main.go
internal/config/
internal/protocol/
internal/natsbus/
internal/discovery/
internal/auth/
internal/bridge/
internal/adapter/
internal/adapter/openclaw/
internal/api/
internal/store/
pkg/types/
```

## daemon 与 Bot 运行模型

daemon 共享一个 HTTP listener、catalog、内容存储、日志和全局执行预算；每个 Bot 独占身份、NATS 连接/订阅、发现、认证/信任、备注、消息日志、Harness 绑定、会话与 workspace。一个 Bot 启动失败记为 failed，不把其他 Bot 的身份或状态借来兜底。业务请求显式带 Bot ID，无默认 Bot。

`bots.json` 是 schemaVersion 2 的持久 catalog，保存 `botId`, `desiredState: running|stopped`, `archived` 及 Console 可选的 `workspaceId`。只加载 catalog 注册项；未知目录不自动激活，列入 daemon health 的 recoveryIssues。每个 Bot 配置/密钥损坏可独立 failed；catalog 本身损坏、工作区认领重复/非法则启动失败。

start/stop 先持久化 desiredState，再异步转换 starting/running/stopping/stopped；API 202 只是接纳，需轮询。显式 stop 持久化 stopped；daemon 退出保留原 desiredState，重启恢复期望 running 的 Bot。archive 必须等待停止，保留全部状态及文件引用，restore 回 stopped，不自动 start。管理接口及 revision CAS 见 [api.md](api.md)。

桌面工作区使用 admin `POST /v2/workspaces/{workspaceId}/binding`，一次 catalog 原子写入同时注册新 Console 和工作区；重试只交付原 Bot 的 scoped token，不替换普通既有身份。对应 Bot 私有 `workspace-binding.json` 与 catalog 交叉校验。绑定失效/归档、密钥或 api.token 丢失均 fail closed；恢复原 0600 文件与一致性 catalog 后重试，不删除目录“重新初始化”。若创建在 catalog 提交前中断，未注册目录保留为 recoveryIssues，匹配的工作区标记阻止重试另建身份；先人工对照一致性备份恢复原认领，不能按 displayName 猜测接管。

## 配置：daemon config.json + 每 Bot config.json

使用 `clawsynapse init` 创建 `~/.clawsynapse/config.json`。加载顺序：CLI > `CLAWSYNAPSE_*` daemon 专属环境变量 > 选中的 JSON > 默认值。`--config FILE` 明确指定配置；否则 `--data-dir ROOT` / `CLAWSYNAPSE_DATA_DIR` 选择 ROOT/config.json。不读 `.env`、旧 YAML、旧无前缀环境变量或 Bot 配置环境覆盖；旧布局必须停止后显式迁移，不能自动混读。

daemon JSON 字段（路径为部署机器实际绝对路径，示例 `/private/clawsynapse` 请替换）：

```json
{
  "schemaVersion": 2,
  "dataDir": "/private/clawsynapse",
  "localApiAddr": "127.0.0.1:18080",
  "adminTokenPath": "/private/clawsynapse/admin.token",
  "allowRemoteAPI": false,
  "natsServers": ["nats://127.0.0.1:4222"],
  "production": false,
  "logLevel": "info",
  "logFormat": "json",
  "logFilePath": "/private/clawsynapse/log/clawsynapsed.log",
  "logAddSource": false,
  "logRotateMaxSizeMb": 100,
  "logRotateMaxBackups": 10,
  "logRotateMaxAgeDays": 14,
  "logRotateCompress": false,
  "maxConcurrent": 8,
  "blobMaxBytes": 10737418240,
  "tempMaxBytes": 1073741824,
  "workspaceMaxBytes": 10737418240
}
```

可选连接字段：`apiTLSCertFile`, `apiTLSKeyFile`, `natsCredentialsFile`, `natsTLSCAFile`, `natsTLSCertFile`, `natsTLSKeyFile`, `natsTLSServerName`。这些是路径/服务器名而非 token 内容。daemon 专属环境支持 `CLAWSYNAPSE_DATA_DIR`, `CLAWSYNAPSE_LOCAL_API_ADDR`, `CLAWSYNAPSE_ADMIN_TOKEN_PATH`, `CLAWSYNAPSE_API_TLS_CERT_FILE`, `CLAWSYNAPSE_API_TLS_KEY_FILE`, `CLAWSYNAPSE_ALLOW_REMOTE_API`, `CLAWSYNAPSE_NATS_SERVERS`, `CLAWSYNAPSE_NATS_CREDENTIALS_FILE`, `CLAWSYNAPSE_NATS_TLS_CA_FILE`, `CLAWSYNAPSE_NATS_TLS_CERT_FILE`, `CLAWSYNAPSE_NATS_TLS_KEY_FILE`, `CLAWSYNAPSE_NATS_TLS_SERVER_NAME`, `CLAWSYNAPSE_PRODUCTION`, `CLAWSYNAPSE_LOG_LEVEL`, `CLAWSYNAPSE_LOG_FORMAT`, `CLAWSYNAPSE_LOG_FILE_PATH`。预算/轮转其余值通过 JSON 或对应 daemon CLI flags 配置。

`--check-config` 严格校验并打印 daemon 配置，不生成身份/凭据。未知字段、旧 schema、无效路径、安全设置失败会拒绝启动。dataDir 必须为绝对非根目录；敏感凭据为私有常规文件，目录 0700，文件 0600。禁止通过 symlink/路径逃逸访问其他 Bot。

BotConfig 保存在 `bots/{botId}/config.json`；通过 admin API/CLI create/config 管理，身份不允许手工指定。以下是完整配置字段示意（revision/bindingRevision 由 GET 返回后保留；新建不必提供 bindingRevision）：

```json
{
  "revision": 1,
  "bindingRevision": "server-generated",
  "displayName": "assistant-a",
  "labels": {"env":"production","caps":["chat","tools"]},
  "trustMode": "explicit",
  "trustAutoApprove": false,
  "heartbeatInterval": "15s",
  "announceTtl": "30s",
  "agentProduct": "clawsynapse",
  "agentVersion": "",
  "harness": {
    "adapter": "opencode",
    "model": "zai-coding-plan/glm-5.3-flash",
    "timeout": "10m",
    "allowedAgentIds": []
  },
  "deliverablePrefixes": ["chat","task"],
  "transferMaxFileSize": 104857600,
  "transferTtl": "24h",
  "maxConcurrent": 2,
  "maxQueued": 64,
  "fileQuotaBytes": 1073741824
}
```

可选 Bot 连接字段 `natsCredentialsFile`, `natsTLSCAFile`, `natsTLSCertFile`, `natsTLSKeyFile`, `natsTLSServerName` 覆盖 daemon 的同项；不覆盖共享 natsServers/listener。Harness 还有 `webhookUrl`, `credentialsFile`, `instance`；凭据是私有文件，不是 JSON 内明文。支持 adapter `console|default|openclaw|opencode|codex|webhook`。Console 无执行 Harness，仅持久收件；不配置 model/instance/webhook/凭据/agent allowlist，健康为不适用。OpenClaw/Webhook 必须显式 instance，同 adapter 的非归档 Bot 不可共享 instance。OpenClaw/default 不允许 per-call model override；OpenCode/Codex model 传到真实原生命令，而非仅展示字段。

更新配置先 stop 并等到停止，再 GET 完整 config、修改、以当前 revision PUT；陈旧 revision 返回 409，不能覆盖他人更新。任何 Harness 变更生成新 bindingRevision 和 `bots/{botId}/workspace/{bindingRevision}`；原生 CLI 在该目录运行。产品 session 再按 Bot、绑定、产品和发送方/目标 Agent/会话作用域隔离，原生 session ID 持久保存。失效 session 明确报错，不静默重建重跑。绑定切换不复用旧会话；工作区不是 OS 沙箱，仍须按模型工具权限和宿主账号限制执行。

displayName、labels、产品/版本写入签名 announce，agentVersion 为空使用构建版本；labels 是自报属性，不表示就绪或授权。聊天参与者是 Bot；DSH 每 Bot 一个产品入口，不配置聊天 Agent 目录。`harness.allowedAgentIds` 只是通用 CLI 内部路由的本地 allowlist。

### 凭据与生产连接

首次 daemon 启动创建 `admin.token`；每个 Bot 创建自己的 `credentials/api.token`。admin 能管理全部 Bot；scoped token 只能访问自己的业务，不能列 catalog、配置/生命周期或导入宿主文件。自定义 API endpoint 必须显式配对应 token。不要把 token 放进 URL、日志或仓库。

非 loopback API 要求 allowRemoteAPI=true 与 API TLS 证书/私钥，客户端信任 CA，不提供跳过验证或明文回退。production 要求 TLS NATS 与身份凭据，拒绝 open 信任、URL 内嵌凭据；可用 credentials 或客户端证书。NATS account/subject ACL 仍由 server/JWT 管理端设置，客户端 TLS 不自动建立隔离。按各 Bot 授权 inbox、必要 discovery/auth/trust 和 ObjectStore API，不应全局放开 `$JS.>`。

普通消息和文件要求实际可用的 JetStream；消息不降级到 Core NATS。持久 outbox 会继续发送已记录 intent，发布失败不代表可安全换 ID 重发。

## 存储、配额与恢复

```text
DataDir/
  config.json                 daemon schemaVersion 2
  admin.token                 daemon 管理凭据
  bots.json                   注册 catalog
  bots/{botId}/
    config.json
    workspace-binding.json     仅工作区 Console；与 catalog 的 workspaceId 核验
    credentials/              identity.key, identity.pub, api.token
    trust.json, replay-cache.json, peer-attrs.json
    state/messages.jsonl       持久消息/outbox/执行状态
    sessions/{bindingRevision}/ 产品会话映射
    transfers/                 持久传输记录
    workspace/{bindingRevision}/
  blobs/                      daemon 私有内容存储，不能作为外部下载路径
```

文件内容物理去重，Bot 的持久引用才是访问权。`fileQuotaBytes` 按该 Bot 引用的不同内容大小计逻辑用量；同内容在其他 Bot 已存在也不能绕过本 Bot 配额。同 Bot 多引用同内容只计一次。daemon 的 blobMaxBytes 限制共享物理内容，tempMaxBytes 限制暂存，workspaceMaxBytes 限制管理的工作区物化；文件流还受 transferMaxFileSize 限制。超限明确失败，不降级为任意路径读取或泄露内容路径；外部模型自行写文件仍需宿主机磁盘配额。

删除 transfer 只删除当前 Bot 的引用。GC 在启动和每 5 分钟运行，先严格读取 schemaVersion 2 的 bots.json，并核验每个注册 Bot（含 stopped/archived）的目录及权威 transfers 引用目录，再扫描所有目录的引用（也包含尚未注册的遗留目录）。仅回收无引用且未被进行中读取 pin 的内容；catalog、注册目录、引用目录缺失或损坏/不安全时中止 mark，保守暂停回收并告警，不能解释为“没有授权引用”。不手工删除共享 blob、catalog 或归档 Bot 状态来腾空间。

运行中的布局校验、文件写入、列表及 export 都要求既有权威引用目录；丢失的 transfers 目录不能自动创建后当成空历史。只有明确的新 Bot 创建/import 事务可以初始化新引用目录。损坏恢复应保留现状并从一致性备份核对 catalog、Bot 目录、引用和内容，不以空目录/空 catalog 静默重置授权历史。

每个 Bot `state/messages.jsonl`（0600）以 fsync 提交发布幂等键/outbox、收到的封套/游标、queued/admitted/done 工作及执行事件。持久挂载整个 DataDir。重启继续 queued 工作和 outbox；崩溃前 admitted 的执行变为 `execution.interrupted` / indeterminate，不自动再次调用模型/外部动作。反馈与文件通知按非执行性语义恢复。当前消息日志不自动裁剪，应规划磁盘和一致性备份；共享 JetStream 消息保留 7 天不等于本地历史裁剪。不能删日志来“修复”不确定执行。

旧数据必须用 `clawsynapse migrate --legacy-config FILE --data-dir ROOT` 显式停机迁移；保存身份、信任、备注、消息日志/会话和文件内容，生成路径映射/校验清单/marker，保留原始备份。目标可为原根或独立空目录，不可嵌套/覆盖其他有效 daemon；不自动合并多个身份。未完成 marker 拒绝启动；保留失败布局和原始数据检查。迁移后 Bot 初始 stopped。Bot export/import 同样要求停止 daemon，包含私钥和状态，按敏感备份处理；操作示例见 CLI 文档。

## 启动流程

daemon 验证 schema/layout 并获取根目录独占锁，打开内容存储、catalog 与唯一 API listener，再恢复期望 running 的 Bot。每 Bot 验证配置/身份匹配，建立独立 NATS 和持久消息上下文，绑定 Harness、订阅其 inbox/控制/发现，发布签名 announce 和心跳。关停先拒绝写入，再等待请求引用与执行退出，保留 desiredState 以供下次恢复。

## 部署

### 前置条件

- 启用 JetStream 的 NATS Server。
- 每 Bot 所选产品的已安装、已认证原生 CLI 或专用 Webhook/OpenClaw 实例；health 探针不替代真实模型任务验收。

```bash
docker run -d --name nats -p 4222:4222 nats:latest -js
clawsynapse init --nats-servers nats://127.0.0.1:4222
clawsynapsed --config "$HOME/.clawsynapse/config.json"
```

### 一键安装为系统服务

推荐把 `clawsynapsed` 安装为宿主机上的长期服务，而不是用交互式 shell 挂起进程。

统一安装入口：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash
```

最佳实践约定：

- 二进制由同一个安装脚本分发，避免 CLI 和 daemon 安装来源不一致
- daemon 服务读取 `~/.clawsynapse/config.json`
- 首次安装生成 daemon 配置，后续升级不覆盖用户配置；旧目录要求先显式停机迁移
- Bot 由 admin 在 daemon 启动后显式创建、配置、启动，不自动生成默认身份
- daemon 以普通用户运行，不以 root 直接执行业务进程
- Bot ID 由各自 Ed25519 身份派生，不能用配置手工指定

如果是自动化环境，显式传参而不是依赖交互输入：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --daemon --nats-servers nats://127.0.0.1:4222
```

安装完成后，推荐通过 CLI 向导做后续配置调整：

```bash
clawsynapse init
clawsynapse version
clawsynapsed --version
clawsynapse init --overwrite --nats-servers nats://127.0.0.1:4222
clawsynapse service restart
```

当前支持的服务管理器：

- Linux: `systemd`
- macOS: `launchd`

仅安装 daemon：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --daemon
```

仅安装 CLI：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --cli
```

卸载：

```bash
./scripts/install.sh --uninstall
./scripts/install.sh --daemon --uninstall
./scripts/install.sh --all --uninstall --purge
```

服务管理：

```bash
# Linux
sudo systemctl status clawsynapsed.service
tail -f ~/.clawsynapse/log/clawsynapsed.log
sudo journalctl -u clawsynapsed.service -f  # 兼容旧安装

# macOS
launchctl print gui/$(id -u)/io.github.yuanjun5681.clawsynapse.clawsynapsed
tail -f ~/.clawsynapse/log/clawsynapsed.log
```

### Release 自动化

推荐把版本发布也收敛成标准流程：

- 用 Git tag 作为唯一版本源，例如 `v0.0.4`
- 测试版 tag 使用 semver prerelease 后缀，例如 `v0.0.4-rc.1`
- 由 `make release-prep` 统一生成 `dist/`、`checksums.txt` 和 release notes
- 由 GitHub Actions 在 `v*` tag push 时自动创建 GitHub Release
- 带 prerelease 后缀的 tag 会自动标记为 GitHub prerelease，不会进入 `releases/latest`

本地预演：

```bash
make release-prep VERSION=v0.0.4
./scripts/release.sh --version v0.0.4 --skip-publish
```

如果你平时长期在 `develop` 上开发，可以直接用脚本切一次正式 release，而不需要自己来回切分支：

```bash
./scripts/cut-release.sh v0.0.4
```

切测试版也可以直接用同一个脚本：

```bash
./scripts/cut-release.sh v0.0.4-rc.1
```

脚本默认会：

- 检查工作区是否干净
- 拉取 `origin/develop`、`origin/main` 和 tags
- 切到 `main`
- 先 fast-forward 到 `origin/main`
- 再把 `origin/develop` 合入 `main`
- 推送 `main`
- 创建并推送目标 tag，例如 `v0.0.4` 或 `v0.0.4-rc.1`
- 最后切回你原来的分支

当 tag 是 `v0.0.4-rc.1`、`v0.0.4-beta.1` 这类 prerelease 版本时：

- GitHub Release 会自动标记为 prerelease
- 一键安装脚本默认使用的 `releases/latest` 不会切到这个版本
- 如需安装测试版，显式指定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --version v0.0.4-rc.1
```

如果只想本地预演，不立即推送：

```bash
./scripts/cut-release.sh v0.0.4 --no-push
```

如果你希望强制要求 release 必须是快进合并，也可以显式开启严格模式：

```bash
./scripts/cut-release.sh v0.0.4 --ff-only
```

本地直接发布：

```bash
./scripts/release.sh --version v0.0.4
```

本地直接发布测试版：

```bash
./scripts/release.sh --version v0.0.4-rc.1
```

CI 自动发布入口：

- 工作流文件：`.github/workflows/release.yml`
- 触发条件：push `v*` tag
- 发布资产：`clawsynapse` / `clawsynapsed` 多平台二进制 + `checksums.txt`

### 多 Bot 与多机部署

一台机器可由同一 daemon 托管 Bot A、B，各自绑定不同 workspace/model/session/凭据；远端机器的 Bot R 通过同一 wire v2 与它们通信。共机不合并信任、alias 或文件授权，也不把本地 Bot 当作无需认证的快捷通道。共享 daemon 带来共同的进程、磁盘与全局预算故障域；需要 OS/租户级隔离时使用不同账号、容器或机器。

## 安全

### 传输层

- 为 NATS 配置 credentials 或双向 TLS，并在服务器限制 account/subject 权限。
- 正式部署启用 production 校验，使用受信任证书的 TLS；不嵌入共享长期明文密码。

### 应用层

- 使用 Ed25519 挑战响应握手验证 Bot 身份
- 对 wire v2 消息与发现声明执行完整签名、时间和重放检查；消息收件箱去重与 admitted 执行恢复持久化，发现新鲜度不是可恢复执行日志。
- 不承诺端到端正文加密、外部副作用 exactly-once 或自动密钥轮换。

### Agent 网关层

- 使用网关 token 或共享凭证连接本地 Agent
- 在转发前校验消息结构与负载大小

### 隔离策略

- 使用 NATS subject 权限限制发布和订阅范围
- 不在 NATS 载荷中传输 API key、token 或私钥

## 方案对比

| 方案 | 耦合度 | 可移植性 | 复杂度 | 身份认证 |
|------|--------|----------|--------|----------|
| Agent 内部插件 | 高 | 仅限特定产品 | 中 | 依赖插件 SDK |
| 网关直连 WebSocket | 中 | 仅限特定产品 | 高 | 依赖网关认证 |
| HTTP Webhook 中继 | 低 | 任意 Agent | 中 | 需自行实现 |
| ClawSynapse + NATS | 低 | 任意 Agent | 低 | Ed25519 内置 |

## 验收边界

接口/隔离契约不等于所有外部产品组合已验收。运行环境应分别确认多 Bot 身份与凭据、显式选择、信任/备注、文件授权/配额/恢复，以及所选真实模型的答案与权威终态。health、CLI 存在、仅有答案 delta 或模型名可配置都不能代替完整任务成功；不要用 indeterminate 触发盲目重跑。

本次实际验收使用 Docker NATS、同 daemon 的 A/B 和外部 R：已覆盖独立身份/scoped 凭据、显式 CLI 选择、信任与 alias 隔离、A 撤销信任不改变 B；真实 OpenCode 模型答案及权威终态；SIGKILL 后 admitted 执行报告不确定且不重跑；共享 blob/可变 workspace 副本隔离、文件引用隔离及归档引用保留。真实 CLI 的停机迁移/export/import 验证了稳定身份、游标、信任、alias、自定义路径、原始备份及可读文件恢复；安装器隔离初始化和 TUI 切换也已实际执行。该证据不扩展为所有故障点、产品版本或操作系统的穷举保证。

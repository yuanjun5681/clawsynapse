---
summary: "ClawSynapse 运行与配置：Go 实现、目录结构、配置项与阶段规划"
title: "ClawSynapse Operations"
---

# ClawSynapse Operations

最后更新：2026-03-12

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

## 并发模型

- 一个 goroutine 管理 NATS 生命周期
- 一个 goroutine 管理节点心跳
- subscription handler 按消息独立处理
- 一个后台清理协程处理过期 peer、握手超时和去重缓存
- 每个外部调用使用 `context.WithTimeout`

## 配置

加载优先级如下：

```text
CLI 参数 > OS 环境变量 > 项目根目录 .env > ~/.clawsynapse/config.yaml > 默认值
```

推荐把长期稳定配置放到 `~/.clawsynapse/config.yaml`：

```bash
mkdir -p ~/.clawsynapse
cp config.example.yaml ~/.clawsynapse/config.yaml
```

```yaml
natsServers:
  - nats://127.0.0.1:4222
localApiAddr: 127.0.0.1:18080
trustMode: tofu
trustAutoApprove: false
agentAdapter: default
agentAdapterTimeout: 10m
heartbeatInterval: 15s
announceTtl: 30s
agentProduct: clawsynapse
# 留空时使用 clawsynapsed 的构建版本
agentVersion: ""
capabilities:
  - chat
  - tools
dataDir: ~/.clawsynapse
identityKeyPath: ~/.clawsynapse/identity.key
identityPubPath: ~/.clawsynapse/identity.pub
deliverablePrefixes:
  - chat
  - task
```

项目根目录下的 `.env` 适合本地开发覆盖，例如：

```bash
cp .env.example .env
```

```bash
NATS_SERVERS=nats://127.0.0.1:4222
HEARTBEAT_INTERVAL_MS=15000
ANNOUNCE_TTL_MS=30000
TRUST_MODE=tofu
TRUST_AUTO_APPROVE=false
AGENT_ADAPTER=default
AGENT_ADAPTER_TIMEOUT=10m
AGENT_PRODUCT=clawsynapse
AGENT_VERSION=
AGENT_CAPABILITIES=chat,tools
DATA_DIR=~/.clawsynapse
IDENTITY_KEY_PATH=~/.clawsynapse/identity.key
IDENTITY_PUB_PATH=~/.clawsynapse/identity.pub
DELIVERABLE_PREFIXES=chat,task
LOCAL_API_ADDR=127.0.0.1:18080
```

`agentProduct`、`agentVersion` 和 `capabilities` 会写入 discovery announce。接入 DeepSeek Harness 时可分别设为 `deepseek-harness`、实际 Harness 版本以及插件真实支持的能力（例如 `chat,task,transfer`）。`agentVersion` 为空时使用 `clawsynapsed` 的构建版本。

Webhook 共享 token 是例外：`WEBHOOK_AUTH_TOKEN` 只从 daemon 的 OS 进程环境读取，不读取 `.env`、YAML 或 CLI，且不会由配置 API 返回。

补充配置项：

```bash
NATS_TOKEN=
NATS_CREDS_FILE=/path/to/creds
TRUSTED_KEYS_DIR=~/.clawsynapse/peers/
BRIDGE_EVENTS=agent_end,message_sent
```

## 启动流程

具体的 subject 命名、认证消息与控制消息字段，以 `docs/protocol.md` 为准。这里仅描述运行时订阅与启动行为。

```text
1. 加载或生成 Ed25519 密钥对
2. 从公钥派生 `did:key` 与 `nodeId`
3. 连接 NATS
4. 连接本地 Agent 网关
5. 订阅本节点 inbox subject
6. 订阅 discovery 相关 subject
7. 订阅 auth / trust 所需控制 subject
8. 发布初始注册信息
9. 启动心跳定时器
10. 开始处理入站消息
```

## 部署

### 前置条件

- 运行中的 NATS Server
- 本地运行中的 Agent 网关

快速启动 NATS：

```bash
docker run -d --name nats -p 4222:4222 nats:latest
```

### 运行守护进程

```bash
clawsynapsed \
  --nats-servers nats://localhost:4222 \
  --agent-adapter openclaw \
  --gateway-url ws://127.0.0.1:18789 \
  --gateway-token "$GATEWAY_TOKEN"
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
- daemon 服务固定读取 `~/.clawsynapse/config.yaml`
- 首次安装由脚本生成配置文件，后续升级不覆盖用户配置
- 交互终端下一键安装脚本可补问 NATS、adapter 等关键参数
- daemon 以普通用户身份运行，不以 root 直接执行业务进程
- `nodeId` 由本地身份密钥自动派生，不通过 CLI 或 YAML 手工指定

如果是自动化环境，显式传参而不是依赖交互输入：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --daemon --nats-servers nats://127.0.0.1:4222 --agent-adapter openclaw
```

安装完成后，推荐通过 CLI 向导做后续配置调整：

```bash
clawsynapse init
clawsynapse version
clawsynapsed --version
clawsynapse init --overwrite --nats-servers nats://127.0.0.1:4222 --agent-adapter openclaw
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

### 多节点异构部署

```text
┌──────────────────┐     ┌──────────┐     ┌──────────────────┐
│ Machine A        │     │          │     │ Machine B        │
│                  │     │   NATS   │     │                  │
│ OpenClaw Gateway │     │  Server  │     │ Custom Agent API │
│ clawsynapsed A ──├─────┤          ├─────┤── clawsynapsed B │
│ (n1-local-id)    │     │          │     │ (n1-peer-id)     │
│ adapter=openclaw │     │          │     │ adapter=custom   │
└──────────────────┘     └────┬─────┘     └──────────────────┘
                              │
                    ┌─────────┴────────┐
                    │ Machine C        │
                    │                  │
                    │ 自研 Agent 服务   │
                    │ clawsynapsed C ─ │
                    │ (n1-third-node)  │
                    │ adapter=custom   │
                    └──────────────────┘
```

## 安全

### 传输层

- 为 NATS 启用 token、NKey 或凭证文件认证
- 跨网络部署时启用 TLS

### 应用层

- 使用 Ed25519 挑战响应握手验证节点身份
- 对点对点消息进行签名校验
- 校验消息时间戳，执行重放保护
- 支持密钥轮换与公钥更新

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

## 实现阶段

### v1

- NATS 连接与订阅
- 节点发现与 peer 表
- 本节点 inbox 收发
- `OpenClawAdapter`
- 本地 `publish` / `peers` API
- `open` / `tofu` 基础信任模式

### v2

- challenge-response 完整握手
- 消息签名与重放保护
- 去重与失败重试

### v3

- 事件转发
- 死信队列
- 观测与诊断接口
- 管理命令集合

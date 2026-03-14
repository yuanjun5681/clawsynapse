# ClawSynapse

语言： [English](./README.md) | **简体中文**

ClawSynapse 是一个面向多 Agent 互联的本地通信网络层。
它以独立 Go 守护进程（`clawsynapsed`）运行在与 Agent 相同的设备上，对外连接 NATS，对内通过适配层调用本地 Agent API。

## 提供能力

- 基于共享 NATS 总线的跨 Agent 消息通信
- 节点发现与 peer 注册表
- 身份认证与信任流程
- 消息签名与重放保护
- 面向 CLI/技能/工具集成的本地 HTTP API

## 架构

```text
Agent <-> Local ClawSynapse Daemon <-> NATS <-> Remote ClawSynapse Daemon <-> Remote Agent
```

## 快速开始

环境要求：

- 可用的 NATS 服务

### 1. 启动守护进程

从 [GitHub Releases](https://github.com/yuanjun5681/clawsynapse/releases) 下载对应平台的 `clawsynapsed` 二进制，然后启动节点：

```bash
clawsynapsed --node-id node-alpha
```

使用 OpenClaw 适配器启动：

```bash
clawsynapsed \
  --node-id node-alpha \
  --trust-mode open \
  --agent-adapter openclaw \
  --openclaw-agent-id main
```

也可通过环境变量配置：

```bash
export NODE_ID=node-alpha
export TRUST_MODE=open
export AGENT_ADAPTER=openclaw
export OPENCLAW_AGENT_ID=main
clawsynapsed
```

使用 `--check-config` 打印最终配置后退出（调试用）：

```bash
clawsynapsed --node-id node-alpha --check-config
```

### 2. 安装 CLI

安装 `clawsynapse` CLI 工具以管理运行中的节点：

```bash
# 从 GitHub Release 一键安装
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash

# 或从本地 dist/ 安装（需先 make dist）
./scripts/install.sh

# 卸载
./scripts/install.sh --uninstall
```

### 3. 使用 CLI 管理节点

```bash
# 检查守护进程健康状态
clawsynapse health

# 列出已发现的节点
clawsynapse peers

# 向远程节点发送消息
clawsynapse publish --target node-beta --message "hello from alpha"

# 发送请求并等待回复
clawsynapse request --target node-beta --message "ping" --timeout-ms 5000

# 对节点发起认证
clawsynapse auth challenge --target node-beta

# 信任流程
clawsynapse trust request --target node-beta --reason "collaboration"
clawsynapse trust pending
clawsynapse trust approve --request-id <req-id>
clawsynapse trust reject --request-id <req-id>
clawsynapse trust revoke --target node-beta

# 查看最近消息
clawsynapse messages
```

全局参数：`--api-addr host:port`、`--timeout duration`、`--json`（输出原始 JSON，便于脚本集成）。

## 配置

配置优先级：`CLI 参数 > OS 环境变量 > 项目根目录 .env > ~/.clawsynapse/config.yaml > 默认值`

默认主配置文件：`~/.clawsynapse/config.yaml`

项目根目录下的 `.env` 会在开发时自动加载。

可直接参考仓库里的 `config.example.yaml` 和 `.env.example` 模板。

常用环境变量：

- `NATS_SERVERS`（逗号分隔）
- `NODE_ID`
- `LOCAL_API_ADDR`
- `DATA_DIR`
- `IDENTITY_KEY_PATH`
- `IDENTITY_PUB_PATH`
- `HEARTBEAT_INTERVAL_MS`
- `ANNOUNCE_TTL_MS`
- `TRUST_MODE`（`open` | `tofu` | `explicit`）

## 文档

- [总览](./docs/overview.md)
- [核心概念](./docs/concepts.md)
- [消息与协议](./docs/messaging.md)
- [信任与认证](./docs/trust.md)
- [集成与适配](./docs/integration.md)
- [CLI 使用](./docs/cli.md)
- [运行与配置](./docs/operations.md)

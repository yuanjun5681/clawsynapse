<p align="center">
  <img src="assets/nanoclaw-logo.png" alt="NanoClaw" width="400">
</p>

<p align="center">
  一个在 Docker 容器中安全运行的个人 AI 助手，支持点对点 Agent 网络通信。
</p>

<p align="center">
  <a href="https://discord.gg/VGWXrf8x"><img src="https://img.shields.io/discord/1470188214710046894?label=Discord&logo=discord&v=2" alt="Discord"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> | <a href="README_CN.md">中文</a>
</p>

---

## 简介

NanoClaw 是一个轻量级、可自托管的 AI 助手，通过 WhatsApp 与你对话。每个会话群组运行在独立的 Docker 容器中，拥有隔离的文件系统和记忆。Agent 可以通过 [Pilot Protocol](https://pilotprotocol.network) 与互联网上的其他 AI Agent 进行加密的点对点通信。

**核心特性：**

- **WhatsApp 交互** — 在手机上给 AI 助手发消息
- **容器隔离** — 每个群组运行在独立的 Docker 沙箱中
- **Pilot Protocol** — Agent 可发现并与其他 Agent 点对点通信
- **定时任务** — 自动运行的周期性作业
- **Agent 团队** — 多个 Agent 协作完成复杂任务
- **独立记忆** — 每个群组拥有隔离的 `CLAUDE.md` 记忆
- **技能扩展** — 通过技能添加 Gmail、Telegram、X/Twitter 等集成

## 环境要求

| 依赖 | 版本 | 用途 |
|------|------|------|
| [Node.js](https://nodejs.org) | 20+ | 宿主进程运行时 |
| [Docker](https://docker.com/products/docker-desktop) | 最新 | Agent 容器运行时 |
| [socat](https://linux.die.net/man/1/socat) | 任意 | Pilot Protocol socket 桥接 (macOS) |
| [Pilot Protocol](https://pilotprotocol.network/docs/getting-started) | 最新 | Agent 间网络通信（可选） |
| [Claude Code](https://claude.ai/download) | 最新 | AI 原生的安装和定制工具 |
| [Rust](https://rustup.rs) | 最新 | 桌面应用构建（可选） |

### 安装依赖

**macOS (Homebrew)：**

```bash
brew install node socat
# Docker Desktop: https://docker.com/products/docker-desktop
```

**Linux (apt)：**

```bash
sudo apt install nodejs npm socat docker.io
```

**Pilot Protocol（可选）：**

```bash
curl -fsSL https://raw.githubusercontent.com/TeoSlayer/pilotprotocol/main/install.sh | sh
pilotctl init --registry 34.71.57.205:9000 --beacon 34.71.57.205:9001
pilotctl daemon start --hostname my-agent
```

## 快速开始

```bash
git clone https://github.com/gavrielc/nanoclaw.git
cd nanoclaw
claude
```

然后运行 `/setup`。Claude Code 会自动处理：安装依赖、WhatsApp 认证、容器构建和服务配置。

## 环境变量

在项目根目录创建 `.env` 文件：

```bash
# 认证（以下二选一，必填）
ANTHROPIC_API_KEY=sk-ant-api03-...        # 按量付费 API 密钥
CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-...  # Claude 订阅 Token

# Agno agent 模型（可选，默认使用 Claude）
AGNO_MODEL_ID=claude-sonnet-4-5-20250514
AGNO_API_KEY=sk-ant-api03-...
AGNO_BASE_URL=https://api.anthropic.com
AGNO_TEMPERATURE=0.7
AGNO_MAX_TOKENS=4096

# 应用设置（可选）
ASSISTANT_NAME=Andy              # 触发词（默认：Andy）
CONTAINER_TIMEOUT=1800000        # 容器超时时间，毫秒（默认：30 分钟）
MAX_CONCURRENT_CONTAINERS=5      # 最大并行容器数
LOG_LEVEL=info                   # debug | info | warn | error
```

> 只有认证变量、`AGNO_*` 和 `PILOT_BRIDGE_PORT` 会传入容器，其他环境变量仅在宿主机使用。

## 构建

```bash
# 安装宿主依赖
npm install

# 编译宿主进程
npm run build

# 构建 Agent 容器镜像
./container-agno/build.sh
```

验证容器：

```bash
docker run --rm --entrypoint pilotctl nanoclaw-agent-agno:latest --json context
```

## 运行

**开发模式：**

```bash
npm run dev
```

**生产环境（macOS launchd）：**

```bash
# 安装为常驻服务
cp launchd/com.nanoclaw.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.nanoclaw.plist

# 管理
launchctl unload ~/Library/LaunchAgents/com.nanoclaw.plist  # 停止
launchctl kickstart -k gui/$(id -u)/com.nanoclaw            # 重启
```

**桌面应用（可选）：**

需要 [Rust](https://rustup.rs)。首次配置：

```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
cd desktop && npm install && cd ..
```

开发模式：

```bash
npm run desktop:dev
```

首次构建需要编译 Rust 依赖，可能需要几分钟。后续为增量编译，速度很快。

**日志：**

```bash
tail -f logs/nanoclaw.log          # 宿主日志
ls groups/*/logs/container-*.log   # 每个容器的日志
```

## 架构

```
WhatsApp ──► Node.js 宿主 ──► Docker 容器 (Agno agent)
               │                    │
               ├── SQLite 数据库     ├── /workspace/group（隔离文件系统）
               ├── IPC 监视器        ├── /workspace/ipc（文件 IPC）
               ├── 任务调度器        ├── pilotctl（Pilot Protocol CLI）
               └── socat 桥接       └── socat ──► 宿主 daemon
                     │
                     ▼
               Pilot Protocol daemon ──► P2P 覆盖网络
```

**宿主进程**（`src/index.ts`）：连接 WhatsApp，路由消息到容器，管理 IPC 和调度。

**Agent 容器**（`container-agno/`）：Python + Agno 框架。每个群组有独立的容器和挂载的工作空间。内置 `pilotctl` 用于 Agent 间通信。

**Pilot Protocol 桥接**：macOS 上 Docker 运行在 Linux VM 中，Unix socket 无法跨 VM 边界。`socat` TCP 桥接（端口 19191）在宿主 daemon socket 和容器之间中继。

### 关键文件

| 文件 | 用途 |
|------|------|
| `src/index.ts` | WhatsApp 连接、消息路由、IPC |
| `src/container-runner.ts` | 容器生命周期、挂载、Pilot 桥接 |
| `src/task-scheduler.ts` | 定时任务执行 |
| `src/db.ts` | SQLite 操作 |
| `container-agno/Dockerfile` | Agent 镜像（Python + Agno + pilotctl） |
| `container-agno/agent-runner/` | 容器内运行的代码 |
| `container-agno/skills/` | 同步到每个 Agent 会话的技能 |
| `groups/*/CLAUDE.md` | 每个群组的持久记忆 |

## Pilot Protocol

[Pilot Protocol](https://pilotprotocol.network) 给你的 Agent 一个 P2P 加密网络上的永久地址。其他 Agent 可以发现并与你的 Agent 通信。

**在宿主机配置：**

```bash
pilotctl daemon start --hostname my-agent
pilotctl info    # 查看地址和 peers
```

**在容器内，Agent 可以：**

```bash
pilotctl --json info                                    # 节点状态
pilotctl send-message other-agent --data "hello"        # 发送消息
pilotctl --json inbox                                   # 查看收件箱
pilotctl send-file other-agent /workspace/group/report  # 发送文件
pilotctl ping other-agent                               # 测试连通性
pilotctl handshake other-agent "collaboration request"  # 建立信任
```

桥接是自动的 — 当宿主机存在 `~/.pilot/config.json` 时，`container-runner.ts` 会启动 socat 桥接并将 `PILOT_BRIDGE_PORT` 传递给容器。容器 entrypoint 创建本地 socket，使 `pilotctl` 透明工作。

## 使用

用触发词（默认：`@Andy`）与助手对话：

```
@Andy 每个工作日早上 9 点发一份销售管线概览
@Andy 每周五检查 git 历史并更新 README
@Andy 向 agent-alpha 发消息询问最新报告
```

在主频道管理群组和任务：

```
@Andy 列出所有群组的定时任务
@Andy 暂停周一简报任务
@Andy 加入家庭群
```

## 定制

直接告诉 Claude Code 你想要什么：

- "把触发词改成 @小助手"
- "让回复更简短直接"
- "每天早上发问候消息"

或者运行 `/customize` 进行引导式修改。

### 可用技能

| 技能 | 用途 |
|------|------|
| `/setup` | 首次安装和配置 |
| `/customize` | 添加渠道、集成、修改行为 |
| `/debug` | 容器问题、日志、故障排查 |
| `/add-telegram` | 添加 Telegram 渠道 |
| `/add-gmail` | Gmail 集成 |
| `/x-integration` | X/Twitter 集成 |

## 贡献

**不要添加功能，添加技能。**

贡献一个技能文件（`.claude/skills/your-skill/SKILL.md`），教 Claude Code 如何改造 NanoClaw 安装。用户运行 `/your-skill` 即可获得针对自身需求的定制代码。

参考[现有技能](.claude/skills/)了解示例。

### 征集技能

- `/add-slack` — Slack 渠道
- `/add-discord` — Discord 渠道
- `/setup-windows` — 通过 WSL2 + Docker 支持 Windows

## 常见问题

<details>
<summary><b>为什么用 WhatsApp？</b></summary>
因为作者用 WhatsApp。Fork 后运行一个技能来切换，这就是 NanoClaw 的设计理念。
</details>

<details>
<summary><b>能在 Linux 上运行吗？</b></summary>
可以。运行 <code>/setup</code>，Docker 会自动配置。
</details>

<details>
<summary><b>安全吗？</b></summary>
Agent 运行在 Docker 容器中，只能看到显式挂载的目录。详见 <a href="docs/SECURITY.md">SECURITY.md</a>。
</details>

<details>
<summary><b>如何调试问题？</b></summary>
运行 <code>claude</code>，然后 <code>/debug</code>。或直接问 Claude："调度器为什么没运行？"
</details>

<details>
<summary><b>Pilot Protocol 是必须的吗？</b></summary>
不是。它是可选的。没有它，你的 Agent 正常工作，只是无法与网络上的其他 Agent 通信。
</details>

## 社区

有问题？有想法？[加入 Discord](https://discord.gg/VGWXrf8x)。

## 许可证

[MIT](LICENSE)

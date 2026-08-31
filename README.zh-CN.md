# ClawSynapse

语言： [English](./README.md) | **简体中文**

ClawSynapse 是一个面向多 Agent 通信的本地网络层。
它会在你的机器上运行本地守护进程 `clawsynapsed`，连接到 NATS，并提供 `clawsynapse` CLI 来发现节点、发送消息和管理信任关系。

## 快速开始

一条命令安装 CLI 和 daemon：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash
```

在交互式终端中运行且首次创建 daemon 配置时，安装脚本会引导你完成必要配置。

## 安装

默认会同时安装 CLI 和 daemon：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash
```

只安装 CLI：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --cli
```

只安装 daemon：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --daemon
```

如果是非交互式安装，建议显式传参：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --daemon --nats-servers nats://127.0.0.1:4222 --agent-adapter openclaw
```

安装脚本会在首次安装 daemon 时创建 `~/.clawsynapse/config.yaml`。后续升级会保留已有配置；如果配置文件已存在，交互安装也不会再次询问这些值。
`nodeId` 不再手工配置；daemon 会根据本地 Ed25519 身份密钥自动派生 `did:key` 和满足 subject 规则的 `nodeId`。

## 升级

升级到最新版本时，直接再次执行同一个安装脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash
```

安装指定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --version v0.0.x
```

如果需要安装测试版，也使用显式版本号，例如：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --version v0.0.4-rc.1
```

升级后建议重启 daemon，确保新版本已经生效：

```bash
clawsynapse version
clawsynapsed --version
clawsynapse service restart
clawsynapse health
```

## 常用命令

检查状态：

```bash
clawsynapse health
clawsynapse peers
```

打开终端面板和日志：

```bash
clawsynapse dashboard
clawsynapse logs
clawsynapse logs --follow
```

新安装默认会把 daemon 日志写到 `~/.clawsynapse/log/clawsynapsed.log`。

发送消息：

```bash
clawsynapse publish --target <peer-node-id> --message "hello from local node"
```

发起认证和信任流程：

```bash
clawsynapse auth challenge --target <peer-node-id>
clawsynapse trust request --target <peer-node-id> --reason "collaboration"
clawsynapse trust pending
clawsynapse trust approve --request-id <req-id>
```

查看最近消息：

```bash
clawsynapse messages
```

## 配置

主配置文件：

```text
~/.clawsynapse/config.yaml
```

如需重新生成或更新配置，可再次运行：

```bash
clawsynapse init
clawsynapse init --overwrite --nats-servers nats://127.0.0.1:4222 --agent-adapter openclaw
clawsynapse service restart
```

如果只想查看 daemon 最终生效的配置：

```bash
clawsynapsed --check-config
```

## 卸载

移除 CLI 和 daemon：

```bash
./scripts/install.sh --uninstall
```

只移除 daemon：

```bash
./scripts/install.sh --daemon --uninstall
```

连同本地配置和数据一起清理：

```bash
./scripts/install.sh --all --uninstall --purge
```

## 更多文档

- [总览](./docs/overview.md)
- [Agent Internet 愿景](./docs/agent-internet.md)
- [CLI 使用](./docs/cli.md)
- [运行与配置](./docs/operations.md)
- [消息与协议](./docs/messaging.md)
- [信任与认证](./docs/trust.md)
- [集成与适配](./docs/integration.md)

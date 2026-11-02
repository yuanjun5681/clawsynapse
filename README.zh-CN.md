# ClawSynapse

语言： [English](./README.md) | **简体中文**

ClawSynapse 是一个面向多 Agent 产品互联的本地网络层。
本地守护进程 `clawsynapsed` 托管多个独立 Bot，各自连接 NATS、发现对端、发送消息并管理信任。Bot 是长期协作身份；Harness（如 OpenCode）是其绑定的执行引擎，内部 Agent 和 Session 不自动成为网络联系人。

## 快速开始

一条命令安装 CLI 和 daemon：

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash
```

首次安装只初始化 daemon 配置，不创建 Bot 或身份。启动服务后，显式创建并启动所需 Bot。

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
  bash -s -- --daemon --nats-servers nats://127.0.0.1:4222
```

首次安装创建 `~/.clawsynapse/config.json`（`schemaVersion: 3`），已有版本 2 数据须先停止 daemon 并显式离线迁移，不在安装或启动时隐式转换。Role 与 Harness 选择属于各 Bot 的配置，不再使用安装参数 `--agent-adapter`。

每个新 Bot 独立生成 Ed25519 密钥，派生 `did:key` 和 `botId = "n1-" + hex(sha256(did)[:16])`。名称、模型、Binding、启停和完整迁移不改变身份；已有密钥损坏时启动失败，不自动替换。wire v2 仍使用同值的 `nodeId/from/to`、既有 subject、签名与 consumer，不要求远端同步升级。

例如准备 `bot-a.json`：

```json
{
  "displayName": "Bot A",
  "role": "agent",
  "harness": {
    "adapter": "opencode",
    "model": "zai-coding-plan/glm-5.3-flash",
    "timeout": "10m"
  }
}
```

在已配置好 OpenCode 及模型凭据的环境中创建 Bot：

```bash
clawsynapse service restart
clawsynapse bot create --file bot-a.json
clawsynapse bots
clawsynapse --bot <返回的完整-bot-id> bot start
```

多个 Bot 可以使用同一 Harness 产品的独立绑定。业务命令始终显式选择 `--bot`，即使只有一个 Bot；完整 ID 或管理员可解析的无歧义 ID 前缀均可，不用公开名称路由。核心真实端到端验证已通过：macOS、Docker NATS 2.11.9、两个 OpenCode 2.2 Binding 使用 GLM 5.3 Flash，覆盖独立身份、权限/信任/备注隔离、原生 Session/工作目录及回复路由、有界并发、单 Bot 停止/恢复、Binding 切换、SIGKILL 后 admitted 任务不盲目重跑，以及共享实体的独立文件授权和可写副本。实际 CLI 迁移/导出恢复及 Go 测试、race、vet 检查也已通过；不代表所有平台、引擎和故障组合均已验证，详见[多 Bot 设计](./docs/multi-bot-daemon.md)。

## 升级

版本 2 多 Bot 布局须先停止 daemon、备份完整 DataDir，再使用新 CLI 显式迁移：

```bash
clawsynapse service stop
clawsynapse migrate --data-dir /absolute/path/to/daemon-root
```

迁移保留身份、信任、消息游标、Agent Binding 与 Session，Console 转为无 Harness 的 `role=console`。中断后先执行 `clawsynapse migrate --recover --data-dir /absolute/path/to/daemon-root`，再重启。旧版本 2 导出包须先用 `clawsynapse bot convert --file /path/to/old-bundle --output /path/to/new-bundle` 显式转换，原包不变。


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
clawsynapse daemon
```

## 常用命令

检查状态：

```bash
clawsynapse daemon
clawsynapse bots
clawsynapse --bot <local-bot-id> health
clawsynapse --bot <local-bot-id> peers
```

打开终端面板和日志：

```bash
clawsynapse --bot <local-bot-id> dashboard
clawsynapse logs
clawsynapse logs --follow
```

新安装默认会把 daemon 日志写到 `~/.clawsynapse/log/clawsynapsed.log`。

发送消息：

```bash
clawsynapse --bot <local-bot-id> publish --target <remote-bot-id> --message "hello from Bot A"
```

发起认证和信任流程：

```bash
clawsynapse --bot <local-bot-id> auth challenge --target <remote-bot-id>
clawsynapse --bot <local-bot-id> trust request --target <remote-bot-id> --reason "collaboration"
clawsynapse --bot <local-bot-id> trust pending
clawsynapse --bot <local-bot-id> trust approve --request-id <req-id>
```

查看最近消息：

```bash
clawsynapse --bot <local-bot-id> messages
```

## 配置

daemon 配置：`~/.clawsynapse/config.json`；各 Bot 配置：`~/.clawsynapse/bots/<botId>/config.json`。

初始化 daemon 配置不创建身份；修改 NATS 默认连接等 daemon 设置可显式重新生成配置：

```bash
clawsynapse init
clawsynapse init --overwrite --nats-servers nats://127.0.0.1:4222
clawsynapse service restart
```

Bot 绑定、信任策略及模型使用 Bot 配置管理；先停止并等待，再以当前修订号提交完整 JSON：

```bash
clawsynapse --bot <local-bot-id> bot stop
clawsynapse --bot <local-bot-id> bot get
clawsynapse --bot <local-bot-id> bot config
# 将 config 响应中的完整配置对象保存并编辑为 bot-update.json
clawsynapse --bot <local-bot-id> bot config --file bot-update.json
clawsynapse --bot <local-bot-id> bot start
```

daemon 管理凭据位于 `admin.token`；Harness 应使用 `bots/<botId>/credentials/api.token`，通过 `--token-file` 和完整 `--bot` 选择自己的业务身份。HTTP 业务路径为 `/v2/bots/{botId}/...`，发送目标为 `targetBot`；旧 `/v1` 单身份路径已移除。Bot token 不能修改配置/生命周期、管理其他 Bot 或导入宿主文件。

信任、备注、消息、执行 Session 和文件引用各自隔离。同宿主不默认互信或共享文件授权；共享 blob 仅减少物理存储，Harness 使用独立工作副本。多 Bot 不是强多租户安全沙箱。

旧单身份安装必须停机、备份后显式迁移，不能直接让新 daemon 加载旧布局：

```bash
clawsynapse migrate --legacy-config /absolute/path/config.yaml --data-dir /absolute/path/target-root
```

迁移保留原身份；同一身份的新旧副本不得同时运行。单 Bot 离线导出/导入使用私有目录包，并包含所有引用的 blob：

```bash
# 先停止整个 daemon；导出要求完整 ID，目录包目标必须尚不存在
clawsynapse --bot <full-bot-id> bot export --data-dir /absolute/path/root --file /private/path/bot-export
clawsynapse bot import --data-dir /absolute/path/other-root --file /private/path/bot-export
```

包中包含私钥和接入凭据，必须按秘密保护。仅复制 `bots/<botId>/` 不是完整备份；导入保留 Bot 身份并轮换本地访问凭据。

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

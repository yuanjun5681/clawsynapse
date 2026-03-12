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
nodeId: node-alpha
natsServers:
  - nats://127.0.0.1:4222
localApiAddr: 127.0.0.1:18080
trustMode: tofu
heartbeatInterval: 15s
announceTtl: 30s
dataDir: ~/.clawsynapse
identityKeyPath: ~/.clawsynapse/identity.key
identityPubPath: ~/.clawsynapse/identity.pub
```

项目根目录下的 `.env` 适合本地开发覆盖，例如：

```bash
cp .env.example .env
```

```bash
NATS_SERVERS=nats://127.0.0.1:4222
NODE_ID=node-alpha
HEARTBEAT_INTERVAL_MS=15000
ANNOUNCE_TTL_MS=30000
TRUST_MODE=tofu
DATA_DIR=~/.clawsynapse
IDENTITY_KEY_PATH=~/.clawsynapse/identity.key
IDENTITY_PUB_PATH=~/.clawsynapse/identity.pub
LOCAL_API_ADDR=127.0.0.1:18080
```

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
2. 连接 NATS
3. 连接本地 Agent 网关
4. 订阅本节点 inbox subject
5. 订阅 discovery 相关 subject
6. 订阅 auth / trust 所需控制 subject
7. 发布初始注册信息
8. 启动心跳定时器
9. 开始处理入站消息
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
  --node-id node-alpha \
  --agent-adapter openclaw \
  --gateway-url ws://127.0.0.1:18789 \
  --gateway-token "$GATEWAY_TOKEN"
```

### 多节点异构部署

```text
┌──────────────────┐     ┌──────────┐     ┌──────────────────┐
│ Machine A        │     │          │     │ Machine B        │
│                  │     │   NATS   │     │                  │
│ OpenClaw Gateway │     │  Server  │     │ Custom Agent API │
│ clawsynapsed A ──├─────┤          ├─────┤── clawsynapsed B │
│ (node-alpha)     │     │          │     │ (node-beta)      │
│ adapter=openclaw │     │          │     │ adapter=custom   │
└──────────────────┘     └────┬─────┘     └──────────────────┘
                              │
                    ┌─────────┴────────┐
                    │ Machine C        │
                    │                  │
                    │ 自研 Agent 服务   │
                    │ clawsynapsed C ─ │
                    │ (node-gamma)     │
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
- `request-reply`
- 去重与失败重试

### v3

- 事件转发
- 死信队列
- 观测与诊断接口
- 管理命令集合

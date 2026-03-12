---
summary: "ClawSynapse 信任与认证：Ed25519 身份、握手流程与信任模式"
title: "ClawSynapse Trust"
---

# ClawSynapse Trust

最后更新：2026-03-12

## 设计目标

ClawSynapse 的 Trust 设计同时解决两个问题：

- 证明对端是谁
- 决定是否允许对端建立业务连接、接收消息或获取更多发现信息

因此本文将身份认证与信任建立拆成两层：

- `身份认证`：确认某个节点确实持有与其公钥对应的私钥
- `信任授权`：确认该节点是否被允许与本节点建立持久关系

认证成功不等于自动获得业务访问权限。生产环境应将“我认识它”和“我允许它接入”视为两个独立判断。

## 身份模型

ClawSynapse 使用基于 Ed25519 的节点身份模型：

- 每个节点首次启动时生成本地密钥对
- 私钥仅保存在本机文件系统
- 公钥通过 announce 广播或控制面交换
- 首次通信前执行 challenge-response 握手
- 后续控制消息和业务消息可附带签名
- 接收侧校验时间戳、随机数和签名，防止重放与冒充

### 本地状态目录

```text
~/.clawsynapse/
├── identity.key
├── identity.pub
├── trust.json
├── replay-cache.json
└── peers/
    ├── node-beta.json
    └── node-gamma.json
```

- `identity.key`：本地私钥，仅本机可读
- `identity.pub`：本地公钥
- `trust.json`：信任、待审批、撤销等持久化状态
- `replay-cache.json`：最近见过的 challenge / nonce / message id 缓存
- `peers/*.json`：缓存的 peer 公钥、指纹、首次见到时间、最后认证结果

## 信任模式

- `open`：不验证签名，仅用于开发和测试
- `tofu`：首次见到时记录公钥，后续严格校验是否发生漂移
- `explicit`：仅接受预配置公钥或受信任控制面分发的公钥

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| `open` | 接受所有消息，不校验签名 | 本地开发、临时联调 |
| `tofu` | 首次见到写入本地指纹，后续严格比对 | 小团队、受控网络 |
| `explicit` | 仅接受预配置或受控分发的公钥 | 生产环境、跨网络部署 |

生产环境建议：

- 禁用 `open`
- 优先使用 `explicit`
- 若使用 `tofu`，必须启用 key mismatch 告警
- 为 NATS 开启认证与 ACL
- 跨网络部署时启用 TLS
- 确保节点时钟同步，例如使用 NTP

## 认证层与信任层

### 身份状态

- `unknown`：从未见过该节点
- `seen`：已收到 announce 或元数据，但尚未完成认证
- `auth_pending`：挑战已发出，等待对端响应
- `authenticated`：挑战校验成功，身份已确认
- `rejected`：签名错误、key mismatch 或策略拒绝
- `expired`：认证信息过期，需要重新认证

### 信任状态

- `none`：无授权关系
- `pending`：已收到信任请求，等待人工或策略审批
- `trusted`：已授予通信权限
- `rejected`：请求被明确拒绝
- `revoked`：曾经授信，后来被撤销

### 关键原则

- `authenticated` 仅表示“身份可信”
- `trusted` 才表示“允许访问业务能力”
- 可以存在 `authenticated + pending`
- 也可以存在 `authenticated + rejected`

## 发现权限模型

ClawSynapse 应对发现信息进行分级暴露，而不是默认全部公开：

| 信息 | 默认可见性 | 说明 |
|------|------------|------|
| `nodeId` | 可选公开 | 便于最基础的路由与审计 |
| `publicKey` 指纹 | 可选公开 | 用于 TOFU / 预检，不应泄露私钥材料 |
| `hostname` / 标签 | 建议受限 | 可按租户、命名空间或 allowlist 控制 |
| 真实业务 endpoint | 仅 trusted 可见 | 避免未授权探测与骚扰 |
| capabilities / service list | 仅 trusted 或同租户可见 | 防止服务枚举 |

推荐做法：

- announce 只广播最小必要身份信息
- 建立 trust 前，不返回真实连接地址
- 高敏感场景下，连 hostname 也只在同租户或 trusted 后暴露

## 加密分层

ClawSynapse 不建议将“公钥私钥加密”笼统地理解为直接用长期身份密钥去加密所有业务数据。更合理的分层是：

- `Ed25519`：用于节点身份、握手签名、审批签名、撤销签名
- `X25519`：用于在双方之间协商临时会话密钥
- `AES-256-GCM` 或 `ChaCha20-Poly1305`：用于加密真实业务流量

### 主要使用场景

#### 1. 身份证明

- 节点首次启动时生成长期身份密钥对
- 私钥仅保存在本地文件系统
- 公钥通过 announce、控制面或预配置方式分发
- 后续所有 trust 相关操作都基于该身份密钥签名

#### 2. 首次认证握手

- 发起方发送 challenge
- 响应方使用本地私钥签名 challenge response
- 发起方使用对端公钥验证签名
- 双方确认彼此确实持有对应私钥

这一阶段解决的是“你是谁”，不是“后续所有消息都用身份私钥直接加密”。

#### 3. 会话密钥协商

- 双方在身份确认后，使用 X25519 协商共享密钥
- 共享密钥只服务于当前连接、隧道或 channel
- 会话结束后可丢弃，降低长期密钥泄露风险

#### 4. 业务流量保护

- 协商出会话密钥后，使用对称加密保护业务数据
- 适用于 RPC、文件传输、事件流、控制消息、心跳与隧道流量
- 对称加密吞吐更高，也更适合长连接和大消息

#### 5. 高风险控制操作签名

- `approve`
- `reject`
- `revoke`
- 注册、解析、策略变更等控制面请求

这些操作应始终要求签名，避免伪造管理动作。

### 推荐原则

- 长期身份密钥只用于认证和签名
- 数据面优先使用临时会话密钥 + 对称加密
- 将 `messageType`、`subject`、`ts` 纳入签名内容，避免跨用途重放
- 支持会话密钥轮换，减少单次泄露的影响范围

### 三层关系图

```text
┌─────────────────────────────────────────────────────────────┐
│ 第 1 层：身份认证                                          │
│ 目标：证明“你是谁”                                         │
│ 手段：Ed25519 challenge-response / message signing         │
│ 输出：peer 状态变为 authenticated                          │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ 第 2 层：信任授权                                          │
│ 目标：决定“是否允许你访问我”                               │
│ 手段：trust request / approve / reject / revoke            │
│ 输出：peer 状态变为 pending / trusted / revoked            │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ 第 3 层：会话加密                                          │
│ 目标：保护“我们之间传输的内容”                             │
│ 手段：X25519 密钥交换 + AES-GCM/ChaCha20-Poly1305          │
│ 输出：加密的 RPC、事件流、文件传输、控制消息与隧道流量     │
└─────────────────────────────────────────────────────────────┘
```

这三层的职责边界应保持清晰：

- 身份认证回答“对端是不是它自己”
- 信任授权回答“我们是否接受它接入”
- 会话加密回答“传输内容是否被保护”

### 典型时序图

```text
Node A                                         Node B
  |                                               |
  | clawsynapse.auth.<targetNodeId>.challenge.request |
  |---------------------------------------------->|
  |                                               |
  | clawsynapse.auth.<targetNodeId>.challenge.response + signature |
  |<----------------------------------------------|
  |                                               |
  | clawsynapse.auth.<targetNodeId>.challenge.ack + signature |
  |---------------------------------------------->|
  |                                               |
  |        身份已确认：A <-> B authenticated       |
  |                                               |
  | clawsynapse.trust.<targetNodeId>.request + reason + signature |
  |---------------------------------------------->|
  |                                               |
  |                     pending                   |
  |                                               |
  | clawsynapse.trust.<targetNodeId>.response(approve) + signature |
  |<----------------------------------------------|
  |                                               |
  |            双方进入 trusted 状态              |
  |                                               |
  | X25519 key exchange                          |
  |<=============================================>|
  |                                               |
  | encrypted session (AES-GCM / ChaCha20)       |
  |<=============================================>|
  |                                               |
```

若直连 challenge 或 trust request 失败，可插入一层控制面中继：

```text
Node A              Control Plane / NATS Inbox              Node B
  |                           |                               |
  | clawsynapse.trust.<targetNodeId>.request                 |
  |-------------------------->|                               |
  |                           |  store pending request        |
  |                           |------------------------------>|
  |                           |                               |
  |                           |  clawsynapse.control.trust.poll |
  |                           |<------------------------------|
  |                           |                               |
  |                           |  clawsynapse.control.trust.response(approve) |
  |                           |<------------------------------|
  | clawsynapse.trust.<targetNodeId>.response(approve)       |
  |<--------------------------|                               |
  |                           |                               |
```

## 挑战响应流程

### 请求 subject

```text
clawsynapse.auth.<targetNodeId>.challenge.request
```

### 响应 subject

```text
clawsynapse.auth.<targetNodeId>.challenge.response
```

### 确认 subject

```text
clawsynapse.auth.<targetNodeId>.challenge.ack
```

### 流程

1. Node A 发送 challenge request，包含 `from`、`publicKey`、`nonceA`、`ts`
2. Node B 校验请求时间窗口与 nonce 去重情况
3. Node B 返回 challenge response，包含 `nonceB` 与对 `nonceA|fromA|fromB|tsA` 的签名 `proof`
4. Node A 校验 Node B 的签名与公钥
5. Node A 回发 challenge ack，对 `nonceB|fromB|fromA|tsB` 进行签名确认
6. Node B 校验成功后，将对端身份状态更新为 `authenticated`

说明：

- 这一步只完成身份认证，不自动建立业务信任
- 若信任模式为 `open`，可跳过 challenge，但不推荐用于生产

### 载荷示例

请求：

```json
{
  "from": "node-alpha",
  "publicKey": "base64url-ed25519-public-key",
  "nonce": "random-32-bytes-base64url",
  "ts": 1741680000000,
  "alg": "ed25519"
}
```

响应：

```json
{
  "from": "node-beta",
  "publicKey": "base64url-ed25519-public-key",
  "nonce": "random-32-bytes-base64url",
  "ts": 1741680000100,
  "proof": "base64url-ed25519-signature-of-nonceA|node-alpha|node-beta|1741680000000"
}
```

确认：

```json
{
  "from": "node-alpha",
  "ts": 1741680000200,
  "proof": "base64url-ed25519-signature-of-nonceB|node-beta|node-alpha|1741680000100"
}
```

## 信任建立流程

在 challenge-response 之上，ClawSynapse 增加显式信任握手：

### 请求 subject

```text
clawsynapse.trust.<targetNodeId>.request
```

### 审批响应 subject

```text
clawsynapse.trust.<targetNodeId>.response
```

### 撤销通知 subject

```text
clawsynapse.trust.<targetNodeId>.revoke
```

### 流程

1. Node A 完成对 Node B 的身份认证
2. Node A 发送 trust request，包含 `from`、`reason`、`ts`、`requestId`、`signature`
3. Node B 将该请求写入本地 `pending`
4. Node B 可按策略自动处理，或由操作者执行 approve / reject
5. 若 approve，则双方状态进入 `trusted`
6. 若 reject，则本地记录为 `rejected`，并向发起方返回拒绝原因

### mutual trust

若 Node A 与 Node B 独立地都向对方发起 trust request，则可触发自动互认：

- 如果双方都已完成身份认证
- 且本地策略允许 `mutual auto-approve`
- 则双方可从 `pending` 或 `none` 直接进入 `trusted`

这适合 agent-to-agent 自动组网，但应允许配置关闭。

## 中继握手

节点之间未必总能直接收发 challenge 或 trust request，因此需要中继路径：

- 优先走直接 subject / 点对点通道
- 直连失败时，回退到控制面 inbox 或 NATS 中继 subject
- 目标节点通过轮询或订阅 inbox 获取待处理请求

建议增加两类控制面能力：

- `control.trust.poll`：拉取并清空待处理的 trust request
- `control.trust.response`：对待处理请求返回 approve / reject

中继只负责转发请求与响应，不应代替终端节点做签名校验。

## 状态机

### 身份认证状态机

```text
┌───────────┐   收到 announce   ┌──────────┐   发起挑战   ┌──────────────┐
│ unknown   ├──────────────────►│ seen     ├────────────►│ auth_pending │
└───────────┘                   └────┬─────┘             └──────┬───────┘
                                     │ 策略拒绝/校验失败         │ 挑战成功
                                     ▼                          ▼
                                ┌──────────┐             ┌──────────────┐
                                │ rejected │             │ authenticated│
                                └──────────┘             └──────┬───────┘
                                                                  │ TTL 过期
                                                                  ▼
                                                             ┌───────────┐
                                                             │ expired   │
                                                             └───────────┘
```

### 信任状态机

```text
┌───────┐   trust request   ┌─────────┐   approve   ┌─────────┐
│ none  ├──────────────────►│ pending ├────────────►│ trusted │
└───┬───┘                   └────┬────┘             └────┬────┘
    │ reject                         │ mutual auto-approve     │ revoke
    ▼                                └───────────────►─────────┘
┌──────────┐                                               ▼
│ rejected │                                          ┌─────────┐
└──────────┘                                          │ revoked │
                                                      └─────────┘
```

## 持久化

信任状态应跨进程重启持久化，至少包含：

- `trusted` 列表
- `pending` 请求
- `rejected` 与 `revoked` 记录
- peer 公钥指纹
- `approvedAt` / `rejectedAt` / `revokedAt`
- 最近一次认证时间与过期时间

建议 `trust.json` 结构至少包含：

```json
{
  "trusted": [
    {
      "nodeId": "node-beta",
      "publicKey": "base64url-ed25519-public-key",
      "fingerprint": "sha256:abcd1234",
      "approvedAt": "2026-03-12T10:00:00Z",
      "source": "manual",
      "mutual": true
    }
  ],
  "pending": [
    {
      "requestId": "req-123",
      "nodeId": "node-gamma",
      "reason": "request access to task queue",
      "receivedAt": "2026-03-12T10:01:00Z"
    }
  ],
  "revoked": []
}
```

## 撤销与密钥轮换

### 撤销

`untrust` 应同时执行：

- 删除本地 `trusted` 记录
- 关闭现有业务会话或拒绝新会话
- 向对端发送 `revoke` 通知
- 在控制面撤销共享的 trust pair 或授权关系

撤销应是单边生效的，即使对端暂时离线，本地也必须立即停止信任。

### 密钥轮换

需要定义明确策略：

- `open`：忽略
- `tofu`：首次写入后若公钥改变，进入 `rejected` 或 `suspicious`
- `explicit`：只有管理员更新 allowlist 后才接受新公钥

推荐在 peer 元数据中保存：

- 当前公钥
- 上一个公钥
- 指纹变更时间
- 变更来源

## 重放保护

仅校验时间戳不够，建议同时使用以下机制：

- `ts`：限制消息年龄，例如最大 5 分钟
- `maxFutureSkew`：限制未来时钟偏移，例如 30 秒
- `nonce` / `challengeId`：每次 challenge 唯一
- `message hash cache`：短时间内拒绝重复消息

建议默认参数：

- `maxMessageAge = 5m`
- `maxFutureSkew = 30s`
- `nonceCacheTTL = 10m`
- `replayCacheMaxEntries = 10_000`

## 协议实现约定

`Trust` 相关的详细协议字段、subject、签名串规范、通用返回格式与错误码，统一定义在 `docs/protocol.md`。

在 `Trust` 范畴内，本文只保留设计层约束：

- challenge、trust request、approve / reject / revoke 都必须是可签名、可审计的控制消息
- `authenticated` 与 `trusted` 必须保持语义分离
- 中继只负责转发，不替代终端节点执行信任决策
- 所有信任状态迁移都应映射到统一协议状态值与错误码

## 实现建议

如果采用当前的 NATS subject 方案，推荐落地顺序如下：

1. 先实现 challenge-response 与本地 peer key cache
2. 再实现独立的 trust request / approve / reject / revoke
3. 为 pending / trusted / revoked 增加持久化
4. 增加 replay cache 与时间窗口校验
5. 最后补充 relay inbox、mutual auto-approve 与策略控制

---
summary: "ClawSynapse 消息系统：消息入口、发现流程与请求回复数据流"
title: "ClawSynapse Messaging"
---

# ClawSynapse Messaging

最后更新：2026-03-12

## 协议边界

本文档关注消息系统如何被使用，而不是定义所有协议字段。

详细协议定义统一收敛到：

- `docs/protocol.md`：subject、Envelope、通用字段、签名规则、错误码
- `docs/trust.md`：认证与信任建立的设计动机、状态机与时序图

因此本文只保留 Messaging 视角下的使用方式与数据流，不重复维护字段级协议表。

## Messaging 模型

ClawSynapse Messaging 基于以下三类通道：

- `点对点 inbox`：节点或 Agent 间的直接消息投递
- `broadcast`：面向 topic 的广播消息
- `events`：系统事件、生命周期事件与审计事件

协议层推荐的 subject 命名、Envelope 字段和消息类型，见 `docs/protocol.md`。

## 节点发现与消息入口

每个 `clawsynapsed` 启动后：

1. 加载或生成 Ed25519 密钥对
2. 连接 NATS
3. 订阅发现相关 subject
4. 发布自身 announce
5. 周期性发送心跳 announce
6. 维护本地 peer 表和 TTL 驱逐

详细的 `discovery.announce` 与 `discovery.depart` 字段定义见 `docs/protocol.md`。

在 Messaging 视角下，Discovery 主要提供两件事：

- 让发送方知道目标节点是否存在、使用哪个 inbox
- 让本地节点维护一个带 TTL 的 peer 目录，供消息投递前查询

其他节点收到 `depart` 后立即驱逐该节点；未收到 `depart` 时，由 TTL 驱逐处理异常关闭场景。

### 生命周期

```text
clawsynapsed 启动
    │
    ├─ 1. 生成或加载 Ed25519 密钥对
    ├─ 2. 连接 NATS
    ├─ 3. 订阅 clawsynapse.discovery.global.announce
    ├─ 4. 发布自身注册信息
    ├─ 5. 启动心跳定时器
    │
    ├─ 收到其他节点注册信息
    │    ├─ 更新本地节点目录
    │    └─ 设置 TTL 驱逐时间
    │
    ├─ TTL 到期
    │    └─ 驱逐 peer
    │
    └─ 关闭时发布 depart
```

## 数据流

### 发送消息

1. 本地 Agent 调用 `clawsynapsed` 的 `publish` API
2. `clawsynapsed` 查询 peer 目录，定位目标节点 inbox
3. 目标节点未认证时先执行握手
4. 对消息签名后发布到目标 subject
5. 对端 `clawsynapsed` 收到消息并验签
6. 对端通过本地 Adapter 投递给 Agent

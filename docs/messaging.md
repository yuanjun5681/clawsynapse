---
summary: "ClawSynapse 消息系统：v2 结构化请求、流式执行、在线投递与恢复边界"
title: "ClawSynapse Messaging"
---

# ClawSynapse Messaging

最后更新：2026-10-20

## 协议边界

本文说明已实现的消息使用与数据流。字段、subject、签名编码见 [协议文档](./protocol.md)，本地访问保护见 [API 文档](./api.md)，产品事件契约见 [集成与适配](./integration.md)。

## 在线投递模型

节点之间使用 Core NATS 点对点 inbox，每条请求或事件是一个完整 JSON 对象。Messaging 与 Discovery 已切换为 v2，不接收旧版封套，也不自动降级；升级时必须协调所有节点与直接集成调用方。

- 请求正文使用 `payload.text` 或 `payload.instruction/input`，不再使用网络 `content` 字段。
- 本地 API 的 `message` 仍是文本快捷输入，映射为 `payload.text`；不能和 `payload` 同时提交。
- 发布成功只说明当前发布调用成功，不等于 broker 持久化、对端接纳或业务完成。
- 断连时拒绝发布，不使用 NATS 重连缓冲积压命令；连接瞬间失效等情况下，结果仍可能不确定。
- 日志只记录关联标识与阶段，不默认记录请求正文。

## 节点发现与消息入口

每个 daemon 加载 Ed25519 身份，连接 NATS，订阅发现及控制 subject，并发布签名 announce、周期续租。

接收 announce 时校验版本、派生身份、完整签名、时效和重放记录；`lastSeenMs` 取本地接收时间。只有已知节点的有效签名 depart 才会驱逐节点，异常退出仍通过 TTL 清理。自报能力不直接授予通信或业务权限。

首次通信应通过本地 auth API/CLI 完成握手，再发起并批准信任请求。不能假设一次 publish 会自动建立首次认证或信任；正常模式下未认证或未受信任的目标会被拒绝。

## 结构化请求与执行事件

```mermaid
sequenceDiagram
    participant A as Agent-1
    participant N1 as Node-1
    participant B as NATS
    participant N2 as Node-2
    participant AD as Adapter / Agent-2
    A->>N1: 本地结构化 publish
    N1->>B: 完整签名 JSON 请求
    B->>N2: 原始消息字节
    N2->>N2: 验签、授权、有效期、去重、排队
    N2->>B: run.started
    N2->>AD: 结构化请求转产品原生调用
    AD-->>N2: 增量答案
    N2-->>B: run.output.delta
    AD-->>N2: 明确终态或异步接纳
    N2-->>B: run.completed / run.failed / run.accepted
    B-->>N1: 按原请求关联的事件
```

- `requestId` 指向原请求消息 ID；`runId` 由接收 Node 生成；`seq` 在单次执行中从 1 递增。
- Codex/OpenCode 原生 JSONL 被 Adapter 增量解析，每个归一化事件再成为一条独立 NATS 消息。OpenClaw Gateway JSON 响应是一次性结果，不宣称原生流式输出。
- delta 仅承载允许发布的答案，不直接透传思考、工具参数和原生 stderr。
- 明确终态不会再次附带已经发出的完整答案；消费者不要重复追加。
- `run.accepted` 仅表示异步接纳，产品运行标识放在 `payload.adapterRunId`；后续结果依赖权威端查询。
- 超时、取消或失败可能发生在副作用之后。`payload.indeterminate=true` 时不自动重新执行原动作。
- run 事件不进入 LLM，也不产生反馈回复；Webhook 可以消费这些事件。

## 有界执行与会话

- 固定 8 个串行工作分片，每分片最多等待 16 条消息；同发送方、Agent、会话进入同一分片，不会相互越序。不同会话可能共享分片，宁可保守串行，也不引入无界队列。
- 过载返回结构化失败；排队后再次检查有效期与节点认证/信任状态。
- 消息默认有效 10 分钟，最大 10 分钟；时效更短的业务必须显式设置 `expiresAt`。
- LLM 产品会话按发送方、目标 Agent 与会话键作用域隔离，避免不同发送方碰撞会话。旧会话失效时明确报错，不静默新建并重跑。
- daemon 关停取消在途执行；CLI runner 限制输出、管理进程树并处理取消。

## 恢复与生产边界

当前 recent messages 最多保留 1000 条，本地 HTTP 最多返回最近 100 条。消息去重和发现新鲜度记录均在内存中；它们不是持久收件箱、执行数据库或可恢复的事件日志。

因此当前不承诺离线投递、重启后的执行去重、无缺口续流或业务 exactly-once。序号只能帮助检测缺口，不能自行补齐。持久状态、查询、回执与恢复属于 [能力路线图](./capability-improvement-roadmap.md) 的 P1。

设备审批、远程启动及文件接收凭据仍须满足 [Camind 规划](./camind-capability-improvement-plan.md) 的全部生产前置要求；通信执行终态不能代替设备端凭据。

---
summary: "ClawSynapse 核心概念：节点、对端、守护进程、适配层与本地 API"
title: "ClawSynapse Core Concepts"
---

# ClawSynapse Core Concepts

最后更新：2026-03-12

## 系统角色

### ClawSynapse

ClawSynapse 是整个网络层系统名，负责将不同 Agent 产品接入到统一的跨节点通信模型中。

### clawsynapsed

`clawsynapsed` 是本地常驻守护进程，负责：

- 持有 NATS 长连接
- 订阅本节点 inbox
- 参与节点发现
- 管理 Ed25519 身份密钥
- 维护认证状态
- 调用本地 Agent Adapter 投递消息

### clawsynapse CLI

`clawsynapse` 是配套 CLI，用于本地诊断、状态查看、配置检查和手动发送测试消息。

### node

`node` 表示网络中的一个实例身份。每个运行中的 `clawsynapsed` 对外注册一个唯一 `nodeId`。

### peer

`peer` 表示当前节点已发现的远端节点。每个 peer 具备身份、公钥、能力、在线状态和认证状态。

## 分层设计

### Transport Layer

负责：

- NATS 连接管理
- 发布与订阅
- 自动重连
- subject 路由

### Protocol Layer

负责：

- 消息 Envelope 定义
- subject 规则
- 节点发现协议
- 认证握手协议
- 签名与重放保护

这些定义的规范真源统一收敛到 `docs/clawsynapse-protocol.md`；本节只描述架构分层，不维护字段级协议细节。

### Bridge Core

负责：

- peer 目录维护
- 认证状态机
- 消息去重
- 路由决策
- 投递失败处理

### Adapter Layer

负责：

- 将统一桥接消息转换为具体 Agent 的投递调用
- 屏蔽 OpenClaw、自研 Agent、其他 Agent 的接入差异

### Local API Layer

负责：

- 给本地 Agent、Skill 或本地工具提供统一入口
- 暴露 `publish`、`peers`、`request` 等能力

## 本地调用边界

本地 Agent 通过 `clawsynapsed` 接入网络层：

- 私钥不暴露给 Agent 运行时
- NATS 连接由守护进程统一管理
- 认证、验签、重放保护逻辑集中
- 减少每个 Agent 产品内重复实现网络协议的成本
- 保持独立 Sidecar 的边界设计

## 节点目录

每个节点在本地维护 peer 目录，用于发现、路由和认证判断。

一个典型的 peer 目录通常包含：

- 节点身份：`nodeId`、`publicKey`、版本、产品信息
- 路由入口：默认 inbox、可用能力、可见性信息
- 认证与信任：`authStatus`、`trustStatus`
- 时序信息：最近 announce 时间、TTL、最后成功通信时间
- 运维信息：失败计数、健康状态、附加 metadata

建议把 peer 目录视为“本地运行时数据模型”，而不是项目级协议真源；涉及字段命名、可见性与消息格式时，应以 `docs/clawsynapse-protocol.md` 为准。

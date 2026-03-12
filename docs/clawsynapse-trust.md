---
summary: "ClawSynapse 信任与认证：Ed25519 身份、握手流程与信任模式"
title: "ClawSynapse Trust"
---

# ClawSynapse Trust

最后更新：2026-03-12

## 身份模型

ClawSynapse 使用基于 Ed25519 的节点身份模型：

- 每个节点首次启动时生成本地密钥对
- 私钥仅保存在本机文件系统
- 公钥通过 announce 广播
- 首次通信时执行 challenge-response 握手
- 后续消息附带签名
- 校验消息时间戳与本地时间偏差，防止重放攻击

## 信任模式

- `open`：不验证签名，仅用于开发和测试
- `tofu`：首次见到自动信任，后续严格校验
- `explicit`：仅接受预配置公钥

生产环境要求：

- 使用 `tofu` 或 `explicit`
- 为 NATS 开启认证
- 跨网络部署时启用 TLS

## 认证状态

每个 peer 在本地维护认证状态：

- `unknown`
- `seen`
- `authenticated`
- `rejected`
- `expired`

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

1. Node A 发送 challenge request，包含 `from`、`publicKey`、`nonce`、`ts`
2. Node B 返回 challenge response，包含新的 `nonce` 和对挑战内容的签名 `proof`
3. Node A 校验 Node B 的签名
4. Node A 回发 challenge ack，对 Node B 的挑战进行签名确认
5. Node B 校验成功后，将对端状态更新为 `authenticated`

## 消息签名

握手完成后，点对点消息附带签名：

```text
sig = Ed25519.sign(privateKey, SHA-256(content + "|" + ts + "|" + from))
```

接收侧执行：

```text
Ed25519.verify(senderPublicKey, SHA-256(content + "|" + ts + "|" + from), sig)
```

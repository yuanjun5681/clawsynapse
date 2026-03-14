# ClawSynapse 实施任务清单

更新时间：2026-03-12

## 已完成

- [x] 项目骨架与 Go 模块初始化
- [x] 配置系统（flag/env/default，支持 `--check-config`）
- [x] 本地状态目录初始化与原子写入
- [x] Ed25519 身份密钥生成/加载
- [x] 协议基础校验（subject/messageType/target/时间窗）
- [x] NATS 总线接入
- [x] Discovery（announce/depart/heartbeat/TTL 驱逐）
- [x] Auth challenge 三段流程（request/response/ack）
- [x] replay cache 持久化与重放拦截
- [x] Trust 流程（request/approve/reject/revoke）与持久化
- [x] 健康检查接口（含 NATS 状态与重连信息）
- [x] Messaging 最小链路（`POST /v1/publish`，trusted 前置校验）

## 进行中

- [ ] 将 Messaging 入站投递到真实 Adapter（当前为本地缓存）

## 待办（P0）

- [ ] OpenClaw Adapter 最小可用接入（ws connect/challenge/chat.send）
- [ ] Relay/Control：`control.trust.poll/response`、`control.auth.poll`
- [ ] 端到端联调测试（双节点/三节点）

## 待办（P1）

- [ ] Prometheus 指标接口与关键指标埋点
- [ ] 发布/订阅（PubSub）基础能力
- [ ] 限流与背压控制
- [ ] CLI 完整化（trust/publish/diagnose）

## 当前可用 API

- `GET /v1/peers`
- `POST /v1/auth/challenge`
- `POST /v1/trust/request`
- `POST /v1/trust/approve`
- `POST /v1/trust/reject`
- `POST /v1/trust/revoke`
- `GET /v1/trust/pending`
- `POST /v1/publish`
- `GET /v1/messages`
- `GET /v1/health`

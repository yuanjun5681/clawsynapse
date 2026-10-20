# Docker e2e（P0 契约验证）

真实容器化验收：`nats:2.11.9`（JetStream + 双向 TLS）+ 两个 `clawsynapsed` 容器 + 模拟 Codex JSONL fixture。

```bash
python3 tests/e2e/runner.py         # 运行并清理
python3 tests/e2e/runner.py --keep  # 保留环境便于排查
```

## 验证范围

- mTLS NATS 连接、签名发现、认证与互信。
- 本地 API Bearer 认证与严格请求解码（未知字段、重复键、尾随 JSON 均拒绝）。
- v2 结构化 payload 端到端一致性：大整数、Unicode、metadata、有效期与签名。
- 流式执行：`run.started → run.output.delta → run.completed` 的 requestId/runId/seq 关联，且 delta 先于终态到达。
- 失败语义：部分输出后失败、非结构化登录提示、超时（indeterminate）、超限事件、未授权 agentId 均不产生 `run.completed`。

## 边界

- `fake-codex.sh` 是受控 fixture，不代表真实 Codex 的认证、模型或事件兼容性。
- 容器环境验证 Unix 进程组路径；Windows Job Object 行为需目标 OS 单独验收。
- 未覆盖真实账号权限、跨主机网络、持久恢复（P1）与设备通道。

---
summary: "Agent Internet 愿景：独立 Bot 通过 ClawSynapse 互联，由 Harness 执行任务"
title: "Agent Internet"
---

# Agent Internet：面向 Agent 的网络协作形态

最后更新：2026-10-31

## 文档定位

本文是一份产品与架构愿景文档，用于描述 ClawSynapse 与执行引擎（Harness）结合后可能形成的网络协作形态。文中以 DeepSeek Harness 为产品示例，也适用于 OpenClaw、OpenCode、Codex 和自研 Harness。对外协作身份称为 Bot；Harness 内部 Agent、subagent 与 Session 不自动成为网络联系人。

本文包含当前能力、近期可实现方向和远期设想。各能力的当前状态以下表为准：

| 能力 | 状态 | 说明 |
|------|------|------|
| Bot 身份、认证、信任审批 | ✅ 已实现 | 见 [trust.md](./trust.md) |
| Bot 发现、心跳、Peer 目录 | ✅ 已实现 | 含 agentProduct 上报 |
| 消息路由、发布/订阅、文件传输 | ✅ 已实现 | 见 [messaging.md](./messaging.md)、[api.md](./api.md) |
| 单 daemon 托管多个独立 Bot | ✅ 已实现，核心端到端验证通过 | macOS / Docker NATS / 两个 OpenCode Binding 已验证身份、权限、执行、文件及恢复隔离；自动化检查通过，环境和边界详见 [多 Bot 设计状态](./multi-bot-daemon.md) |
| 远端任务状态机（accepted/running/blocked 等） | ❌ 未实现 | 阶段 3 |
| AgentCard / CapabilityManifest | ❌ 未实现 | 阶段 4，当前仅有 agentProduct 字段 |
| 细粒度授权 CapabilityGrant | ❌ 未实现 | 阶段 5 |
| 信誉、计费、结算、仲裁 | ❌ 未实现 | 阶段 6 |

本文提到的功能落地时，须在同一 PR 更新此表。

协议字段、subject 和错误码的当前规范真源仍是 [protocol.md](./protocol.md)。认证与信任模型见 [trust.md](./trust.md)，本地 Agent 接入方式见 [integration.md](./integration.md)。

## 核心判断

DeepSeek Harness 和 ClawSynapse 解决的是两个不同层次的问题：

- DeepSeek Harness 解决一个 Agent 如何运行：模型调用、Agent Loop、工具执行、会话、工作区、权限和 subagent。
- ClawSynapse 解决多个 Bot 如何互联：发现、身份认证、信任授权、消息路由、异步协作和文件传输；各 Bot 通过自己的 Binding 使用 Harness。

两者结合后，最大的可能性不只是“给 Harness 增加一个发送消息的插件”，而是：

> 让每个 Bot 成为网络中可寻址、可认证、可授权、可委派任务的协作主体，由其绑定的 Harness 执行工作；让 ClawSynapse 成为 Bot 之间的身份层、信任层、路由层和协作协议层。

本文将这种产品形态称为 **Agent Internet**。

## 什么是 Agent Internet

Agent Internet 不是让 Agent 互相发送自然语言消息这么简单。一个完整的 Agent 网络需要表达：

- 谁在发起请求。
- 请求应该交给哪个 Bot，以及其绑定 Harness 内获准使用的 Agent 或会话。
- 对方具备什么能力。
- 对方是否被授权执行这项操作。
- 任务处于什么状态。
- 任务消耗多少时间、算力和预算。
- 结果、文件和证据来自哪里。
- 任务失败、超时、重复或取消时如何恢复。
- 多个 Agent 的工作如何汇总、验收和审计。

因此，Agent Internet 的基本单元不是聊天消息，而是带身份、权限、状态、上下文和结果的协作任务。

```text
Human / Enterprise Application / Automation
                     │
                     ▼
             Agent Organization Layer
       planning / delegation / review / budget
                     │
                     ▼
              Collaboration Protocol
 task / progress / artifact / result / cancel / verify
                     │
                     ▼
          Capability and Trust Control Layer
 identity / discovery / authorization / policy / quota
                     │
                     ▼
             ClawSynapse Network Layer
       routing / messaging / events / file transfer
                     │
                     ▼
             Bots with Harness Bindings
 DeepSeek Harness / OpenClaw / OpenCode / custom runtime
```

## 以 DeepSeek Harness 为 Agent Runtime

DeepSeek Harness 可以作为 Bot 的一种执行引擎。它在自身运行边界内负责：

- 创建、恢复和销毁 Agent。
- 维护 Session 和上下文。
- 组织模型请求与 Agent Loop。
- 注册并执行工具。
- 管理工作区、文件和命令执行。
- 创建进程内 subagent 或 workflow。
- 执行权限检查和人工审批。
- 将执行过程记录为可观察的事件。

ClawSynapse 在 Harness 执行边界外负责：

- 为每个 Bot 提供长期网络身份，而不是给 daemon 或每个内部 Agent 创建联系人身份。
- 按本地 Bot 视角发现对端并维护 Peer 目录。
- 完成 challenge-response 身份认证。
- 管理信任请求、批准、拒绝和撤销。
- 将消息路由到目标 Bot。
- 在不同 Runtime 产品之间转换投递方式。
- 传输任务制品和文件。
- 为跨 Bot 审计提供稳定的消息与身份边界。

两者结合后的推荐边界是 Sidecar + Plugin：

```text
┌──────────────── DeepSeek Harness ────────────────┐
│                                                 │
│ Agent / Session                                 │
│      ▲                                          │
│      │ followup / inject / event                │
│      ▼                                          │
│ ClawSynapse Harness Plugin                      │
│  ├─ model-visible tools                         │
│  ├─ inbound task router                         │
│  ├─ session mapping                             │
│  ├─ policy and approval hooks                   │
│  └─ optional network console                    │
└─────────────────┬───────────────────────────────┘
                  │ loopback HTTP API
                  ▼
        ┌─────────────────────────┐
        │ clawsynapsed            │
        │ Bot runtimes:           │
        │ identity / trust / NATS │
        │ messaging / transfer    │
        └────────────┬────────────┘
                     │
                     ▼
             Other Bots
```

`clawsynapsed` 应继续作为独立 Sidecar 宿主运行，可以托管多个 Bot。Harness 插件只通过 `/v2/bots/{botId}/...` 和该 Bot 的作用域凭据使用网络能力，不应复制私钥管理、NATS 连接、认证和信任状态机，也不应持有 daemon 管理 token。

## Agent Internet 的核心实体

### Daemon 与 Bot（wire Node）

Daemon 是本地宿主进程，不是网络联系人。Bot 是长期对外协作身份，独立拥有 Ed25519 密钥、DID、信任、消息和文件引用；一个 daemon 可以托管多个 Bot，每个 Bot 只有一个生效的 Harness Binding。

`botId` 沿用 `did:key` 派生算法：`"n1-" + hex(sha256(did)[:16])`。它与 wire v2 的 `nodeId` 是同一个值，协议 seam 保留 `nodeId/from/to`、subject、签名和 durable consumer 契约。名称、模型、Binding、启停及完整迁移不改变身份。

### Harness 与内部 Agent

Harness 执行任务，可在内部组织 `security-reviewer`、`backend-engineer` 等 Agent。内部 Agent 是执行角色，不自动获得 Bot 身份或独立网络信任。可选 `agentId` 只在所选 Binding 的本地允许范围内路由，不选择另一个 Bot 或 Harness。

Peer 是当前本地 Bot 视角的对端 Bot。同一个远端在 A/B 中可以有不同信任与备注；同宿主不默认互信。共享执行进程不构成强多租户沙箱；共享不可变 blob 也不授予另一 Bot 访问权。

### Session

Session 表示执行引擎的持续上下文，不是联系人。ClawSynapse 的外部 `sessionKey` 映射到 Bot 与 Binding 修订隔离的 Harness Session，同一发送方在 A/B 使用同名会话键不会共享上下文。未来协作会话模型不得绕过这一执行边界。

### Capability

Capability 表示 Agent 可以提供的能力，例如：

- `code.review`
- `linux.build`
- `cuda.inference`
- `browser.research`
- `finance.aggregate`
- `production.deploy`

Capability 应描述能力本身、输入输出约束、运行环境、授权条件和成本，而不仅是一个自由文本标签。

### Task

Task 是跨 Agent 协作的主要单元。它至少需要目标、状态、输入、发起者、接收者、期限、幂等键和验收条件。

### Artifact

Artifact 是任务产生或消费的制品，例如补丁、报告、日志、图片、数据集和构建产物。Artifact 应具备来源、内容摘要、MIME 类型、大小、访问策略和生命周期。当前本地不可变内容可跨 Bot 去重，但授权引用、传输记录和可写工作副本独立；信任不自动创建引用。备份/导出须包含引用实体，不能只复制 Bot 目录。

## 基础协作流程

一个异步任务可以遵循以下生命周期：

```text
task.propose
      │
      ▼
task.accepted ───────────────► task.rejected
      │
      ▼
task.running
      │
      ├────────► task.progress
      ├────────► task.blocked
      ├────────► artifact.available
      │
      ▼
task.completed ──────────────► task.failed
      │
      ▼
task.verify
      │
      ├────────► task.approved
      └────────► task.rework
```

建议每个状态事件都携带：

- `taskId`
- `correlationId`
- `from`
- `to`
- `agentId`
- `sessionKey`
- `idempotencyKey`
- `parentTaskId`
- `delegationDepth`
- `deadline`
- `budget`
- `traceId`

对长时间任务，应采用“快速接受 + 异步进度 + 最终结果”，避免通过一个长时间不结束的同步 HTTP 请求等待 Agent 完成。

## 十种应用场景

### 1. Agent 邮箱与远程任务

对应阶段：1–3。

这是最直接、最适合近期落地的产品形态。安装 ClawSynapse Harness 插件后，Agent 可以：

- 查看在线 Peer 和信任状态。
- 向远端 Agent 发送消息。
- 委派任务并获得接受确认。
- 接收进度、阻塞原因和最终结果。
- 发送与接收文件。
- 恢复一个跨 Bot 长期协作上下文，并映射到各自隔离的执行 Session。

用户界面可以表现为 Agent Inbox，而不是普通聊天窗口：

```text
Agent Inbox

● Linux Build Agent
  task.progress 65%
  ARM64 构建完成，正在执行测试

● Security Review Agent
  task.blocked
  需要读取依赖锁文件，请求额外权限

● Design Agent
  artifact.available
  dashboard-v3.fig
```

这一形态可以被理解为 Agent 之间的 Email、Slack 和异步 RPC 的组合。

### 2. 跨 Bot 人工审批

对应阶段：3–5。

Agent 网络自动流转的任务，一旦触及生产部署、敏感数据、大额预算或跨组织委派，就应该把决定权交还给人。以下是阶段 3–5 的未来设想，不是当前已实现的人员身份或审批协议：人工审批跨 Bot 化后，人可以通过经授权的 Bot 接入参与网络协作，而不只依赖某个 Harness 产品的本地 UI。

```text
Release Bot（云端）             运维负责人使用的 Bot（本地）
      │                                │
      ├── approval.request ──────────►│ 操作内容/影响范围/预算/截止时间
      │                                │ 人工审阅
      │◄───────── approval.granted ──│ 或 approval.denied
      │                                │
      └── 携带一次性授权继续执行，或取消并通知 ──►
```

这一形态的关键约束：

- 审批请求必须携带完整的操作描述、影响范围和预算，而不是一句"是否允许"。
- 批准结果是一次性、限范围、限时限的授权，不扩大长期信任。
- 超时未审默认拒绝；批准与拒绝都进入审计，可追溯到原始发起者和完整委派链。
- 人员使用的 Bot 同样要经过认证与信任审批；人的登录身份、实际审批权限与 Bot 的网络持钥身份必须分别校验，不能把 Bot 认证等同于人员授权。

这让"人工接管"从每个 Agent 产品各自的 UI 补丁变成协作协议的一等能力，与"安全与治理"一章的防线一一对应。

### 3. 分布式 Agent 团队

对应阶段：3 起。

Harness 原生 subagent 适合同一进程、同一任务内部的短期分工；ClawSynapse 可以将团队扩展到不同机器、不同 Runtime 和不同权限环境。

```text
Coordinator Agent
 ├─ Product Agent       product workspace
 ├─ UI Agent            macOS / design tools
 ├─ Backend Agent       Linux / internal database
 ├─ Test Agent          isolated test cluster
 ├─ Security Agent      read-only source access
 └─ Release Agent       production approval boundary
```

团队成员拥有独立身份、工作区、权限和模型。协调 Agent 只获得委派权，不需要直接持有所有底层权限，由此形成职责分离和最小权限的数字团队。

### 4. Agent 原生 CI/CD 与任务集群

对应阶段：3–4。

一个 Harness Coding Agent 完成修改后，可以由其 Bot 将后续工作分发给绑定不同执行角色的对端 Bot：

1. 将补丁发送给 Linux Test Agent。
2. Test Agent 运行测试并返回日志。
3. Security Agent 执行静态分析和安全审查。
4. Documentation Agent 更新说明。
5. Release Agent 等待人工批准并发布。
6. Monitoring Agent 观察发布后的指标。
7. 出现异常时，将诊断上下文发回原开发 Agent。

与传统 CI/CD 相比，流程步骤不一定全部预先写死。Agent 可以理解中间结果、动态调整计划、请求协助、解释失败并尝试修复。本场景是场景 10 数字组织的早期具体形态。

### 5. 个人 Agent 星座

对应阶段：2 起。

一个用户可以在不同设备上运行多个长期 Agent：

- 笔记本上的 Coding Agent。
- 家庭服务器上的文件整理 Agent。
- 云端的长期研究 Agent。
- NAS 上的照片和文档 Agent。
- 公司电脑上的内部系统 Agent。
- 手机或家庭设备上的提醒 Agent。

这些 Agent 不需要共享全部数据，而是通过受控任务协作。例如云端 Agent 可以请求个人财务分析，本地隐私 Agent 不返回原始流水，只在本地计算并返回汇总。

该形态支持“代码靠近数据运行”，使个人能够在保留数据控制权的前提下使用云端和本地 Agent 的组合能力。

### 6. 企业 Agent 联邦

对应阶段：5。

企业内部通常存在多个权限域和数据域，不适合构建一个拥有全部权限的超级 Agent。更现实的形态是 Agent Federation：

```text
Engineering Agent Network
          ↕ scoped trust
Finance Agent Network
          ↕ scoped trust
External Vendor Agent Network
```

联邦策略可以规定：

- 研发 Agent 可以请求财务汇总，但不能读取原始流水。
- 外部供应商只能提交制品，不能读取内部代码。
- 安全 Agent 可以审计跨域任务，但不能修改业务数据。
- 高风险操作必须经过人工批准。
- 某类文件只能在指定 Bot 之间传输。
- 某些模型不得处理特定数据等级。

ClawSynapse 的 Trust 可以由简单 Peer 信任逐步演进为基于能力、资源、时间、次数和数据等级的授权。

### 7. Agent 能力市场

对应阶段：6。

Agent、团队或组织可以向可信网络公布可提供的能力：

- 法律文档审查。
- 多语言翻译。
- GPU 推理。
- 代码安全审计。
- 视频生成。
- 数据清洗。
- 工业设备诊断。

任务流程可以扩展为：

```text
task.propose
      ▼
task.quote
      ▼
task.accept
      ▼
task.progress
      ▼
artifact / result
      ▼
task.verify
      ▼
task.settle
```

早期不必使用真实货币，可以先使用企业内部额度、Token 预算、GPU 分钟、调用次数或部门成本中心。长期可以演进出 SLA、信誉、竞价、验收、仲裁和执行凭证。

身份可信不等于能力可信，因此市场必须同时解决能力验证、结果验收、信誉积累和责任追踪。

### 8. 跨模型协作网络

对应阶段：4。

能力路由的通用模型：调用方 Agent 查询 AgentCard 目录，按能力、在线状态和信任过滤，按负载、成本、数据位置排序，向首选发送任务；拒绝或超时则回退下一候选。路由决策在调用方本地完成，ClawSynapse 提供目录与约束过滤，不做中心化调度。历史成功率和组织策略分别在阶段 6（信誉）和阶段 5（联邦）加入约束集合。

模型是 CapabilityManifest 的一个维度。不同模型在推理、成本、速度、隐私和多模态方面各有所长，Agent 网络可以将模型差异表达为可路由的能力：

```text
Original Task
 ├─ small model: classify and preprocess
 ├─ local model: process private data
 ├─ reasoning model: plan and reason
 ├─ domain model: specialist review
 └─ verifier agent: validate the result
```

协调 Agent 可以通过其 Bot 根据步骤难度和数据敏感度选择不同对端 Bot，实现成本、延迟、质量和隐私之间的动态平衡。

这一形态最终可能形成一个 Agent Compute Grid：任务不再固定发送到某个模型 API，而是按能力和约束在网络中调度。

### 9. 现实世界的 Agent 设备网络

对应阶段：4–5。

Bot 的 Harness 接入可以进一步延伸到机器人、无人机、车辆、摄像头、实验室仪器、工厂设备和边缘计算盒子；Bot 身份不等同于设备本身。

例如维修 Agent 可以：

1. 请求摄像头 Agent 拍摄设备。
2. 请求传感器 Agent 读取温度和振动。
3. 请求文档 Agent 查找维修手册。
4. 综合信息生成诊断方案。
5. 将操作建议发送给现场人员或机器人。
6. 将结果和传感器证据归档。

此时网络中传输的不只是自然语言，还包括事件、遥测、文件、控制命令和设备状态。控制现实设备的能力必须采用比普通聊天更严格的授权和安全策略。设备遥测和事件流属于高频流，依赖新增流式通道，见文末适用边界。

### 10. Agent 组织与数字公司

对应阶段：6。

远期可以形成由多个长期 Agent 构成的数字组织：

```text
Goal / Executive Agent
           │
     Planning Agent
 ├─ Research Department
 ├─ Engineering Department
 ├─ Operations Department
 ├─ Finance Department
 └─ Audit Department
```

上层 Agent 不直接完成所有工作，而是设置目标和预算、拆分工作、选择执行者、跟踪状态、处理冲突并验收结果。

这类系统必须避免无限递归委派和任务风暴，因此至少需要：

- 最大委派深度。
- 完整委派路径。
- 任务 TTL 和租约。
- 总预算和并发上限。
- 循环检测。
- 取消传播。
- 失败升级和人工接管。
- 独立审计 Agent。

## 面向未来的协议对象

为了支撑以上场景，ClawSynapse 可以从 Message Envelope 逐步扩展出以下协作对象：

| 对象 | 作用 |
|------|------|
| `AgentCard` | 描述 Agent 身份、能力、环境、限制和可用状态 |
| `CapabilityManifest` | 定义能力输入输出、授权条件、成本和版本 |
| `TaskContract` | 定义目标、输入、预算、期限和验收标准 |
| `TaskState` | 描述任务从提议到完成、失败或取消的状态 |
| `ContextCapsule` | 携带完成任务所需的最小上下文 |
| `ArtifactRef` | 引用文件、补丁、报告、数据集和构建产物 |
| `CapabilityGrant` | 表示针对能力、资源和期限的临时授权 |
| `ExecutionReceipt` | 记录谁在何时使用什么能力执行了什么工作 |
| `ResultAttestation` | 对结果来源、验证过程和签名进行证明 |

例如 AgentCard 可以表达为：

```json
{
  "agentId": "gpu-researcher",
  "nodeId": "n1-example",
  "capabilities": ["python", "cuda", "model.evaluate"],
  "environment": {
    "os": "linux",
    "gpu": "A100"
  },
  "constraints": {
    "maxTaskMinutes": 60,
    "requiresApproval": true
  }
}
```

这些对象不应一次性全部进入底层协议。建议先稳定 Task、Artifact、Capability 和 Grant 四类核心模型，再根据真实产品需求演进。

## 寻址模型

当前 `botId`（wire `nodeId`）用于 Bot 级路由。以下是远期逻辑地址设想，用于描述所选 Bot 背后的能力、内部 Agent 或 Session，不引入与 Bot 平行的聊天身份，也不改变当前网络 v2。

远期可以采用类似以下的逻辑地址：

```text
agent://<bot-did>/agents/security-reviewer
agent://<bot-did>/sessions/project-42
capability://code.security-review
topic://project-42/build-events
```

其语义分别是：

- 向某个 Bot 所绑定 Harness 内的指定 Agent 投递，仍受本地路由授权约束。
- 恢复某个长期 Session。
- 由网络选择具备某项能力的 Agent。
- 向项目或组织的事件 Topic 发布。

实际 NATS subject 可以继续保持当前安全、有限的语法，逻辑地址由 ClawSynapse 在路由层解析，不必直接映射为未经约束的 subject。

## 上下文不等于完整聊天记录

跨 Agent 协作不应默认复制完整 Session。完整上下文可能包含秘密、无关信息、受版权或合规约束的数据，也会增加 Token 成本和 Prompt Injection 风险。

推荐使用 `ContextCapsule`：

- 只包含完成子任务需要的信息。
- 明确说明来源和生成时间。
- 对敏感字段进行分级和脱敏。
- 携带允许使用的目的和有效期。
- 引用大型 Artifact，而不是内嵌全部内容。
- 由接收端重新验证外部内容。

Harness 插件可以在发送任务前生成 Context Capsule，在接收任务后将其作为明确标记的外部上下文注入 Agent，而不是伪装成系统指令。

## 安全与治理

Agent 联网会将单机 Prompt Injection 扩大为网络级风险。一个恶意或被攻陷的 Agent 可能发送诱导指令、制造反馈循环、请求敏感文件或不断委派昂贵任务。

Agent Internet 至少需要以下防线：

### 身份不等于授权

通过 Ed25519 验证身份只说明消息来自某个密钥持有者，不表示它有权执行部署、读取文件或调用付费能力。每个高风险能力仍需独立授权。

### 网络输入始终是不可信输入

来自可信 Peer 的自然语言、Artifact 和 metadata 仍然可能包含错误或恶意内容。Harness 应将其标记为外部输入，并通过工具权限和工作区边界限制影响范围。

### 预算和递归限制

每个任务应具备时间、Token、费用、并发和委派深度上限。子任务消耗应计入父任务预算。

### 幂等、去重和重放保护

任务执行需要稳定的 `idempotencyKey` 和持久化去重记录。网络重试不能导致部署、付款或文件修改被重复执行。

### 来源与审计

任务、上下文、Artifact、工具调用和最终结果应能够追踪到原始发起者及完整委派路径。

### 人工接管

信任批准、生产操作、敏感数据读取、大额预算和跨组织委派应支持人工审批、暂停、取消和撤销。

## 可能形成的产品

### Agent Network Console

在 Harness 中展示本地 Bot、对端 Bot、内部 Agent 路由提示、能力、信任关系、任务状态、文件和审计事件，是最适合作为第一款产品的形态；内部 Agent 不作为额外聊天联系人。

### Agent Teamspace

围绕项目组织多个长期 Agent，提供共享任务图、Artifact、进度、预算和人工协作界面。

### Capability Router

接收能力需求和约束，从网络中选择合适 Agent，并处理故障转移、重试和结果汇总。

### Enterprise Agent Gateway

在企业边界执行跨域授权、数据分级、审计、配额、模型限制和供应商接入。

### Agent Capability Marketplace

提供能力发布、发现、询价、执行、验收、信誉和结算。该形态应在身份、授权和任务协议稳定之后再建设。

## 推荐演进路线

### 阶段 1：Harness 网络工具

- 实现 ClawSynapse Harness Plugin。
- 包装所选 Bot 的 `/v2/bots/{botId}/health`、`peers`、`publish` 和文件传输 API；daemon 聚合健康 `/v2/health` 不是 Bot 业务入口。
- 给模型提供查看 Peer、发送消息和发送文件的工具。
- 高风险工具接入 Harness 审批机制。

### 阶段 2：双向 Agent Inbox

- 通过 Webhook Adapter 将远端消息投递到 Harness。
- 建立 `agentId` 和 `sessionKey` 路由。
- 支持新建 Session 和恢复长期 Session。
- 增加异步接受、进度和最终结果。

### 阶段 3：标准任务协议

- 增加 `taskId`、`correlationId`、`idempotencyKey` 和 `parentTaskId`。
- 定义 accepted、running、blocked、completed、failed 和 cancelled 状态。
- 建立任务超时、取消、重试和去重语义。
- 将 Artifact 与 Task 关联。

### 阶段 4：能力发现与路由

- 将 Discovery 中的产品和能力信息改为可配置、可验证的数据。
- 引入 AgentCard 和 CapabilityManifest。
- 支持按能力而非固定 Bot 选择执行者。
- 增加负载、成本和数据位置约束。

### 阶段 5：组织策略与联邦

- 引入细粒度 CapabilityGrant。
- 支持组织、项目和外部供应商边界。
- 增加配额、审计、数据分级和策略执行。
- 支持任务完整委派链和执行凭证。

### 阶段 6：市场与自治组织

- 在可信身份、任务协议、结果验收和审计成熟后，再引入信誉、报价、结算、仲裁和自治组织能力。

## 适用边界

并非所有 Agent 协作都需要进入 Agent Internet：

| 场景 | 推荐机制 |
|------|----------|
| 同一 Harness 任务内的临时分工 | Harness subagent 或 workflow |
| 同一进程内服务组合 | Cordis Service 或 Event |
| 确定性的数据库、SaaS 或基础设施操作 | 普通 Tool、API 或 MCP |
| 跨 Agent 产品或跨进程通信 | ClawSynapse |
| 跨机器、跨网络或跨组织通信 | ClawSynapse |
| 需要 DID、认证、信任审批和审计 | ClawSynapse |
| 高频 Token 级实时流 | 需要在现有协议上新增流式通道 |

ClawSynapse 不应替代 Harness 内部高效的进程内协作；Harness 也不应复制 ClawSynapse 的跨 Bot 身份和网络协议。两者保持清晰边界，才能分别演进而不相互绑定。

## 结语

DeepSeek Harness 可以成为 Agent Internet 中的一种执行引擎，为其绑定的 Bot 提供模型、工具、会话、工作区和执行循环。ClawSynapse 可以成为 Bot 之间的 Agent Fabric：提供身份、发现、信任、路由、任务和 Artifact 传输。

近期最有价值的落点是“远程任务委派、状态追踪和结果回收”，而不是无结构的 Agent 聊天。以此为基础，系统可以逐步演进为能力路由器、分布式 Agent 团队、企业 Agent 联邦，最终形成由身份、能力、任务、信任和制品连接起来的 Agent Internet。

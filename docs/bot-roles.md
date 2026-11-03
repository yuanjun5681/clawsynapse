---
summary: "Bot Role 实现契约：统一协作身份、角色配置、消息与文件处理、离线迁移及验收边界"
title: "Bot Role"
---

# Bot Role

- 日期：2026-11-02
- 状态：已实现显式 `console`／`agent` Role、可选 Harness 与 schemaVersion 3；验证范围和外部前置条件见第 9.1 节。
- 范围：ClawSynapse 核心领域模型、消息处理、Bot 管理与桌面工作区的对齐。
- 原则：用最简单的设计解决实际问题；共用通信与持久化机制，仅在配置校验、运行资源装配和业务消息分派处处理必要的角色差异。

## 1. 决策摘要

> Bot 是具有独立通信身份、能够收发消息并按自身逻辑处理消息的协作主体。一个 Bot 只有一个 Role。

1. 保留 Bot 作为统一概念，不恢复 Node 作为另一套产品领域模型。已有 wire `nodeId` 保持不变。
2. Daemon 托管多个 Bot；每个 Bot 独立持有身份、密钥、信任、消息与状态。
3. Role 表示主要协作方式，不是另一份身份、权限或产品类型。
4. 当前落地 `console`、`agent`；`device` 是明确的后续方向，真实设备接入前不提供空实现或可创建选项。
5. Agent Harness 只属于 Agent Role 的执行配置。Console 不再通过 `harness.adapter=console` 表达角色。
6. 共用一套 Bot Runtime，集中处理角色配置、资源装配与业务分派差异，不建立完整的角色子类体系。
7. 不引入 Behavior Binding、能力注册中心、通用插件框架、多角色组合或多 Harness 编排。

## 2. 术语

| 术语 | 定义 |
|---|---|
| Daemon | 托管多个 Bot 的本地进程，负责配置、生命周期与共享资源管理；自身不是公共通信身份 |
| Bot | 具有独立通信身份的协作主体，不等同于机器、进程或大模型程序 |
| Role | Bot 的主要协作方式；一个 Bot 只有一个 Role，不使用 `roles[]` |
| Agent | 接收目标、理解上下文并自主执行任务的软件主体，不等于其执行环境 |
| Agent Harness | 支撑 Agent 运行的执行环境，负责模型交互、工具调用、会话等执行职责；下文简称 Harness |
| Harness Binding | Agent Bot 到一个生效执行接入的配置关系，不是通信身份 |
| Adapter | 在执行 Seam 上将统一投递转换为具体 Agent 产品调用的实现 |
| Peer | 相对于当前本地 Bot 观察到的对端 Bot，不是额外身份类型 |

OpenCode、Codex CLI、Claude Code、OpenClaw 共同称为 **Agent 产品**。它们的产品范围不必相同；在 ClawSynapse 中，其执行入口可以承担 Agent Harness 的职责。描述 Codex 接入时应注明具体产品形态，不能从 CLI 接入推广到其他形态。

文档首次使用 Agent Harness 全称，后文可简称 Harness；配置保留简洁字段名 `harness`。不将 Agent Harness 简称为 Agent。列举产品不代表已有 Adapter 或已通过兼容性验收。

## 3. Role 与结构

| Role | 协作方式 | 默认消息处理 | 专属配置 |
|---|---|---|---|
| `console` | 人通过客户端参与协作 | 持久收件，供人查看；不自动执行任务 | 无 Harness |
| `agent` | Agent 自主处理任务 | 按现有消息规则调用 Harness | 一个生效的 Harness Binding |
| `device` | 设备提供确定的功能 | 接收设备指令、返回状态或上报事件 | 首个真实设备接入时确定 |

```text
Daemon
  ├── Bot：role=console
  ├── Bot：role=agent → Codex CLI
  ├── Bot：role=agent → OpenCode
  └── Bot：role=device → 设备程序（后续接入）
```

每个 Bot 共用身份、信任、网络、消息、文件授权与生命周期机制。执行队列、执行预算、Harness 探针和原生 Session 只为 Agent 创建；Console 不构造空 Harness，也不实现一组永远返回“不支持”的执行方法。公共收件持久化、消费排序和文件传输仍保留各自必要的资源限制，不等同于 Harness 执行资源。

Role 在创建时确定，初期不提供普通配置编辑中的 Role 切换。更换 Agent Bot 的模型或 Harness 仍属于执行配置更新，不改变 Role 与 Bot 身份。

Role 不是权限：Agent 不天然获得任务执行授权，Device 不允许任意对端控制。网络业务继续由认证、信任与实际执行端检查决定授权。daemon 仅提供 loopback 管理，无 token 的本机请求可管理任何 Role 的 Bot；显式 Bot scoped token 仍限制调用范围，但本机任意进程都能省略 token，因此不构成本机进程隔离。

## 4. 配置表达

以下为当前创建配置的最小示例；公共可选字段按角色填充默认值，完整字段见 [运维](./operations.md)：

```json
{
  "displayName": "我的控制台",
  "role": "console"
}
```

```json
{
  "displayName": "编程助手",
  "role": "agent",
  "harness": {
    "adapter": "codex"
  }
}
```

### 4.1 创建与校验

- 新建请求必须显式提供单值 `role`；缺失、空值、`null`、未知值或 `roles[]` 均拒绝。未实现的 `device` 也明确拒绝，不按 Agent 兜底。
- 先检查字段是否适用，再按角色填充默认值，最后执行完整校验。不先创建带默认 Harness 的配置再覆盖角色。
- `console` 必须省略 `harness`；显式提交 `null`、`{}` 或任何 Harness 内容均报错，不静默忽略。执行专属的 `maxConcurrent`、`maxQueued` 同样必须省略；文件配额、传输限制等公共字段继续适用。
- `agent` 必须提供非空 Harness 对象，并显式指定受支持的 `harness.adapter`；不从 Role、产品名或探测结果猜测 Adapter。超时等可选执行字段只对 Agent 填充默认值，继续沿用具体 Adapter 的校验与 Binding 隔离规则。
- 持久配置与管理请求使用相同的角色约束；解码必须区分字段缺失与显式空值，不能把多余配置反序列化为零值后绕过校验。
- 不按产品增加 Role；接入新 Agent 产品时增加 Adapter，而不是增加 `role=codex` 等角色。不提前定义通用设备配置或 Device Harness。

### 4.2 更新与修订

配置更新继续采用完整替换与 `revision` CAS，并要求 Bot 停止。更新必须保留原 Role；即使同时删除或补齐 Harness，也不能通过普通更新切换角色。缺少 Role 不表示“保持原值”，而是无效配置。

`revision` 是配置并发控制标识，不是 schemaVersion。Agent 的 `bindingRevision` 仍由服务器管理：实际执行配置变更继续遵守既有修订与 Session 隔离规则；只增加显式 Role 的版本迁移不产生新 Binding。Console 不为满足公共 Interface 而创建 Harness Binding；既有文件路径的处理见第 8 节。

## 5. 消息处理

保留一个处理流程：

```text
收到消息
  → 认证、授权、去重、持久化
  → 按现有协议语义分类并分派
  → Bot 的角色处理逻辑
  → 保存处理结果，按协议返回消息或执行反馈
```

配置校验与 Runtime 资源装配之外，角色差异集中在业务消息分派处；网络、信任、持久化和文件授权不分别维护三套实现。分类依据现有消息 `type`、投递前缀与 Adapter 契约，不根据正文猜测聊天或任务意图。

### 5.1 消息分类与反馈

| 收到的内容 | Console Bot | Agent Bot |
|---|---|---|
| 普通聊天 | 持久收件，供人查看，不创建虚假 Run | 按现有投递规则交给 Harness |
| 执行请求 | 明确拒绝，不停留在永久等待状态 | 交给 Harness 执行 |
| 执行反馈 | 持久保存并关联原请求 | 保存并关联原请求，不自动触发新的模型执行 |

保留既有接入显式观察反馈的契约，例如 Webhook 消费反馈；不因 Role 迁移改变其语义。反馈观察不等于再次发起 Agent 执行，也不生成针对反馈的新执行反馈。

Console 的收件成功不等于任务完成。执行反馈仍由 daemon 根据实际执行结果校验、记录、生成封套并签名，客户端不能伪造。人工任务受理、完成与关联回复不因引入 Console Role 自动成为已实现能力。

不为所有 Role 强制定义执行、取消、恢复 Session 等巨大 Interface。复用现有 Agent Harness 执行 Seam；只有发生真实变化的地方才增加必要分支。

### 5.2 Console 执行拒绝

Console 在公共校验与持久收件之后、执行 admission 与调度之前处理执行拒绝，不等待或占用 Bot／daemon 的 Harness 执行预算：

1. 已通过认证与授权的执行请求若在处理时过期，沿用 `execution.expired`；否则返回 `run.failed`，`payload.code="console.execution_unsupported"`、`indeterminate=false`。
2. 拒绝事件沿用现有 `requestId`、`runId`、`seq`、Session 关联和签名封套；`runId` 仅用于关联拒绝，不表示曾启动 Harness。
3. 不为拒绝生成 `run.started`、`run.accepted`、答案或 `run.completed`。旧 Console Adapter 路径先发出 `run.started` 再失败的行为已移除；消费者应直接关联拒绝终态。
4. 拒绝结果与工作终态先持久提交，再由 outbox 发布；重复投递或进程重启使用原事件与幂等标识恢复，不再次执行或生成第二个拒绝终态。

普通聊天与反馈不进入执行 admission，也不因任务过期逻辑生成虚假 Run。公共收件和拒绝仍受原有认证、授权、去重及存储约束，Role 不绕过这些检查。

### 5.3 文件接收与通知

文件传输是公共通信职责，不因 Console 没有 Harness 而停止：

- `transfer.available` 继续进入现有文件接收、授权、配额与持久化流程，不作为 Agent 任务处理。
- 文件完成后的本地 `transfer.received` 通知也必须按角色分派，不能在回调中绕过分派直接调用 Harness。
- Console 保存可恢复的文件记录与授权引用，客户端通过已有文件列表及读取入口查看；不为通知创建 Harness、执行 Run 或任务完成反馈。桌面关闭不影响 daemon 收件，重开后能读取记录与文件。
- Agent 保留现有文件通知、工作副本和 Adapter 投递契约，不因本次迁移增加通知重放或重复执行。

文件已持久接收、通知已处理与任务已完成是三个不同状态。移除 Console Harness 不得删除既有文件或使其授权路径失效，也不因此为客户端提供共享内容存储路径。

## 6. 统一管理与工作区

统一提供 Bot 创建、列表、配置、启动、停止及已有归档管理。身份、网络、信任、消息与文件记录的管理方式不按角色分叉。

角色专属内容保持直接：

- Console 显示人工收件语义，不显示模型或 Session 配置；Harness 健康为“不适用”，不是“不健康”。
- Agent 显示 Harness 配置与执行状态，生命周期、网络和 Harness 健康继续分别表达。
- Device 的配置与设备状态在真实接入时确定，不从设备在线推断动作已完成。

桌面工作区绑定一个专用 `role=console` 的 Bot，日常发送身份固定；工作区绑定不是 Harness Binding。桌面退出后 daemon 继续收件，重开按原 cursor 回放。

联系人可以是不同 Role 的 Bot。远端声明的 Role 不代表它当前健康、支持某个具体动作或允许当前身份操作；未提供或未知的远端 Role 按“未知”展示，不拒绝既有合法通信，也不自动当作 Agent。本地配置仍严格拒绝未知 Role；本次设计不新增远端 Role 发现机制。

保持既有 Conversation、Turn、Delivery、Run 模型：主题不是网络群聊，成员资格不自动广播内容、共享执行上下文或授予文件权限。

## 7. 设备与具身智能

Role 按协作方式划分，不按硬件载体划分：

- 仅接受明确动作指令的机械臂属于 Device Bot。
- 能理解目标、规划并调用工具的具身 Agent 属于 Agent Bot，即使运行在机器人上。
- Agent 控制一个需要独立寻址与授权的设备时，可以是 Agent Bot 与 Device Bot 两个协作主体。

不因为同一机器人内部有多个程序就强制创建多个 Bot。只有需要独立通信身份、授权或运行管理时才拆分。一台设备可以承载多个 Bot，硬件部件也不必全部获得网络身份。

设备接入时必须定义实际指令、授权、安全约束与完成语义，不能仅凭 Role 将 Agent 的任务反馈直接视为物理动作完成证明。本文不预建设备协议。

## 8. 与当前实现的迁移关系

版本 3 使用显式 `BotConfig.Role` 与可选 `BotConfig.Harness`：Console 无 Harness，Agent 持有一个 Harness Binding。旧版本 2 的 `harness.adapter=console` 只由显式迁移器接受；正常 API、配置读取和普通导入不兼容旧表达。当前具体入口见 [CLI](./cli.md)、[API](./api.md) 与 [运维](./operations.md)。

### 8.1 改造范围

实现保持最小改造：

1. 在现有 Bot 配置增加单值 `role`，按角色校验 Harness 是否适用。
2. 将现有 Console 配置显式迁移为 `role=console`，移除 Harness 及执行专属字段；现有执行接入迁移为 `role=agent`。除第 5.2 节明确调整的拒绝事件序列外，保留既有消息契约。
3. 在配置校验、Runtime 资源装配和业务消息分派处落实角色差异，覆盖 inbox 与文件完成回调，移除 Console 对执行 Adapter 的伪装，不重建一套通信 Runtime。
4. 管理 Interface 与桌面显示 Role，并按角色展示专属配置；不增加通用能力查询框架。
5. 在明确的配置版本迁移中完成切换，不长期保留新旧两种角色表达，也不因探测失败自动创建替代身份。

迁移必须保留 Bot ID、DID、密钥、信任、消息 ID、outbox、幂等键、cursor 与工作区绑定。已有 Agent Binding 与原生 Session 的关联不因纯配置结构调整而失效，不自动重放历史任务。实际更换 Harness 时继续遵守既有 Binding 修订与 Session 隔离约束。

网络 wire `nodeId`、subject、签名规则不因新增本地 Role 重命名；也不为本次设计预先扩展网络发现协议。Role 不是发送或寻址所需的第二个标识。

### 8.2 版本与旧数据入口

当前持久布局使用版本 3：daemon `config.json`、`bots.json` 与 Bot 导出包 manifest 的 `schemaVersion` 同步升级。Bot 配置格式由所属布局或导出包的版本确定，不另建独立版本序列。消息 journal、原生 Session、文件记录及网络 wire 不因本次调整升版。

- 正常启动、配置更新与普通导入只接受新格式，不在读取时猜测旧配置的 Role，也不接受新旧表达混用。
- 版本 2 数据通过显式离线迁移转换。只有迁移器可依据旧 `harness.adapter` 推导 Role；未知 Adapter、损坏配置或无法核验的身份／状态应阻止提交，不以默认 Agent 或新身份替代。
- 新版本普通导入拒绝版本 2 导出包，并提示先进行显式转换。转换在独立暂存位置校验原 manifest 与文件完整性，生成版本 3 导出包；保留原包，不在导入过程中隐式修改它。未知或更高版本直接拒绝。
- 旧单 Bot 数据仍走显式迁移入口，最终生成新布局；不为其保留另一套可运行的旧配置路径。

### 8.3 提交与中断恢复

迁移复用现有离线暂存、备份、持久标记与恢复思路；整个 DataDir 是一次布局切换，不逐 Bot 发布新配置：

1. 停止 daemon，取得覆盖迁移源、目标与所有 Bot 的独占锁，拒绝并发启动、管理写入及迁移。检查所有 catalog 条目，包括停止与归档 Bot，并核验身份、工作区认领、状态及文件引用；损坏或缺失时先修复，不按空历史转换。
2. 在同一文件系统的暂存布局中完成全部转换，保留原数据与权限。只改变角色表达与必要版本字段；Agent 保留原 `bindingRevision`、执行参数、Session 映射及工作目录，不调用普通 Harness 更新逻辑。Console 的历史文件路径与引用仍可解析，不把“移除 Harness”当作删除其工作目录或文件的理由。
3. 完整校验新布局，持久记录恢复所需的源、暂存、备份位置与迁移阶段；同步文件与目录后，才进入整体发布。发布复用目录重命名与原布局备份，持久提交标记完成前不得开放 Runtime 或管理写入，不能把多次重命名视为一个原子操作。
4. 任意阶段中断后，启动必须先检查迁移状态；未完成时拒绝正常运行，要求显式恢复。恢复根据持久标记完成发布或恢复原布局，不加载新旧混合数据，不新建身份，不调用 Harness。
5. 恢复可重复执行；完成迁移后再次调用不得重复转换或改变 Binding。新布局接收新写入之后，不允许直接用迁移前备份回滚覆盖新历史。

迁移不通过清空 pending work、outbox 或去重记录来获得“干净启动”。首次启动继续按既有恢复规则处理 queued／admitted 工作：已完成的拒绝重发原 outbox 事件；已经 admitted 且无法确定结果的历史执行保留不确定性，不改写为“从未执行”的 Console 拒绝，也不自动重跑。

## 9. 实施验收

以下是持续验收要求；代码交付不等于所有真实 Harness、平台及崩溃点均已验证。本轮执行记录见第 9.1 节：

- **配置校验**：新建缺失、空值、`null` 或未知 Role 均拒绝；Console 显式提交任意 Harness（含 `null`、`{}`）或执行预算字段均拒绝；Agent 缺少有效 Adapter 均拒绝。合法 Console 默认配置不包含执行字段，Agent 继续通过各 Adapter 的校验。
- **配置更新**：完整替换与 revision CAS 保持有效；运行中更新、过期 revision 与普通 Role 切换被拒绝，失败不改变原配置、身份或 Binding。
- **Console 迁移与收件**：恢复原身份、凭据和工作区绑定；桌面关闭期间 daemon 仍持久收件，重开按原 cursor 回放。无 Harness、执行调度与探针，不调用模型；健康显示不适用。
- **Console 拒绝**：未过期执行请求得到可关联的 `console.execution_unsupported`，过期请求得到 `execution.expired`，两者均不产生新的 `run.started`、accepted、答案或 completed，不占用执行预算。拒绝持久提交后在发布前崩溃，恢复仍只发布原事件；重复投递不生成第二个拒绝终态。
- **反馈隔离**：Console 普通聊天与反馈只持久收件，不伪造完成；Agent 的反馈不触发二次模型执行。既有 Webhook 反馈消费保持有效，不形成回复循环。
- **文件接收**：Console 在桌面关闭期间完成受授权的文件接收；重开后文件记录与字节可读，迁移前的文件仍可访问。本地完成回调不调用 Harness、不生成执行反馈；Agent 保留文件通知及工作副本契约，已 admitted 通知不自动重放。
- **Agent 连续性**：迁移后真实调用原 Harness；原 Binding 修订、执行参数和原生 Session 映射不变，同一 Conversation 继续使用原有执行上下文。实际更换 Harness 仍创建新 Binding，不误接旧 Session。
- **权限与隔离**：不同 Role 继续遵守原有凭据作用域、认证、信任和文件授权；同 daemon 不自动互信，也不共享文件权限。缺失或未知的远端 Role 不被推断为 Agent，不影响原有合法通信。
- **迁移与恢复**：所有 Bot（含 stopped／archived）一并校验；在暂存完成、原目录备份后、新布局发布后但完成标记提交前分别中断，均阻止普通启动。显式恢复后只出现完整旧布局或完整新布局；重复恢复、重复迁移不换身份、不重建 Binding、不丢失文件引用。
- **版本入口**：新布局使用版本 3；普通启动与导入拒绝旧格式、混合格式及未知版本。旧版本 2 导出包通过显式转换后可导入且身份、历史与文件完整，原包保持不变；旧单 Bot 入口最终输出新格式。
- **副作用恢复**：保留 outbox、幂等键、cursor 与逐目标 Delivery 状态；历史 admitted 执行保留既有不确定性，不伪装为确定拒绝，不自动重放历史副作用。
- **未实现角色**：Device 不可创建，不作为可用能力展示，也不借助空实现通过验收。

### 9.1 本轮验证记录

- 自动检查：受影响包测试、`go test -race ./...`、`go vet ./...` 与 `go build ./...` 通过；迁移回归覆盖正常 loader 支持的局部 daemon 配置，未提供的存储预算使用既有默认值。
- 实际运行：在独立 Docker JetStream 与临时 daemon 上验证配置拒绝、Console 持久聊天、只有 `run.failed` 的执行拒绝、幂等发布、文件接收与授权读取、重启后原工作区身份和 token 恢复；默认调试 Adapter 的 Agent 路径实际执行成功，不将其作为真实模型验收。
- 离线迁移：用隔离 daemon 数据构造合法版本 2 布局，实际 CLI 升级后身份、消息 journal 字节、Agent Binding、归档 Bot 与文件保持有效；重复升级不改写配置。实际完成旧导出包转换、原包字节不变、旧包普通导入拒绝及转换后导入和文件读取。
- 终端：实际运行 dashboard，观察角色选择器中的 Console Role，验证 Console 配置页不展示 Harness 或执行预算。
- 外部限制：真实 OpenCode 已被 daemon 调起，但模型请求失败；直接运行本机 CLI 返回 `provider.auth`、HTTP 401。因此原生 OpenCode Session 的跨迁移真实模型连续性尚未验收，需有效的 provider 凭据后再验证；未跳过权限确认或把失败任务改为成功。


## 10. 文档归属

本文是 Bot Role 模型与实现契约的单一说明，不复制当前协议或完整管理规范：

- [核心概念](./concepts.md)：当前领域概念与角色边界。
- [单 Daemon 多 Bot 设计](./multi-bot-daemon.md)：已实现的多身份托管、隔离、生命周期与历史验收；本文收窄 Harness 的角色适用范围，不推翻其通信与隔离设计。
- [协议](./protocol.md)、[消息](./messaging.md)、[API](./api.md)、[集成](./integration.md)、[CLI](./cli.md)、[运维](./operations.md)：当前事件序列、配置、迁移与恢复入口。

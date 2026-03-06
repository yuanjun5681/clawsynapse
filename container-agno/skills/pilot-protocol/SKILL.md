---
name: pilot-protocol
description: Send and receive messages to/from other AI agents on the Pilot Protocol network. Use pilotctl to communicate with peers, check inbox, send files, and manage trust relationships.
allowed-tools:
  - "Bash(pilotctl:*)"
---

# Pilot Protocol

You have access to `pilotctl`, a CLI tool for communicating with other AI agents on the Pilot Protocol peer-to-peer network.

## Available Commands

### Messaging
```bash
# Send a message to another agent
pilotctl send-message <agent-name> --data "your message" --type text

# Send JSON data
pilotctl send-message <agent-name> --data '{"key":"value"}' --type json

# Check your inbox
pilotctl --json inbox

# Clear inbox after reading
pilotctl inbox --clear
```

### File Transfer
```bash
# Send a file to another agent
pilotctl send-file <agent-name> /path/to/file

# Check received files
pilotctl --json received

# Clear received files
pilotctl received --clear
```

### Network & Discovery
```bash
# Check your node info (address, hostname, peers)
pilotctl --json info

# Ping another agent
pilotctl ping <agent-name>

# View trusted peers
pilotctl --json trust
```

### Trust Management
```bash
# View pending handshake requests
pilotctl --json pending

# Approve a handshake
pilotctl approve <agent-name>

# Reject a handshake
pilotctl reject <agent-name>
```

### Self-Discovery
```bash
# Get full command reference as JSON schema
pilotctl --json context
```

## Collaboration Rules

### Receiving Messages from Peers

1. **Read before reply** — 收到消息通知后，先用 `pilotctl --json inbox` 读取完整内容，处理并回复后执行 `pilotctl inbox --clear` 清除收件箱
2. **Auto-handle when safe** — 简单问答、状态查询、公开信息等可以直接回复，不需要通知用户
3. **Notify user only when needed** — 以下场景必须用 `send_message` 告知用户并等待确认：
   - 发送文件或数据给 peer
   - 修改本地文件或配置
   - 代替用户做决策
   - 访问用户的私人信息
   - 协作完成时发送最终结果摘要
   - 超时或异常情况

### Sending Messages to Peers

1. **User-initiated only** — 只有用户明确要求时才主动向 peer 发送消息，不要自主决定联系其他节点
2. **Keep messages concise** — 每条消息说清一件事，避免长篇大论
2. **State your intent** — 消息开头说明目的（请求/回复/通知），例如：
   - `[请求] 能否帮我查一下...`
   - `[回复] 结果如下...`
   - `[通知] 任务已完成`
3. **Include context** — peer 不知道你的对话历史，提供足够背景信息

### Conversation Lifecycle

1. **Start** — 发起协作时，告知用户你要联系哪个 peer、做什么
2. **Progress** — 如果协作超过 2 轮消息往返，给用户发进度更新
3. **Completion** — 根据角色判断协作是否完成：
   - **主动方**（你发起请求）：收到的回复满足了你的原始需求，即为完成
   - **被动方**（你收到请求）：你已回复了对方需要的信息或结果，即为完成
   - 如果对方要求澄清或追问，继续回复，不算完成
4. **Close** — 完成后发送 `[结束]` 前缀的结束语（如 `[结束] 感谢协作`），然后不再回复。收到对方的 `[结束]` 消息后也不需要回复
5. **Timeout** — 如果 60 秒内没有收到 peer 回复，告知用户并询问是否继续等待

### Trust Management

1. **Handshake requests** — 收到信任请求时，展示对方信息和理由，让用户决定 approve 或 reject
2. **Never auto-approve** — 不要自动批准任何信任请求

## Important Notes

- Always use `--json` flag for structured output when processing results programmatically
- The daemon runs on the host machine; you connect to it via a mounted socket
- Do NOT run `pilotctl daemon start/stop` or `pilotctl init` - the daemon is managed by the host
- Messages are stored in the recipient's inbox until they read them

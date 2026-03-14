---
name: clawsynapse
description: >
  Use this skill when the user wants to contact, message, or collaborate with
  another person, agent, or node — even if they don't mention "ClawSynapse"
  directly. Covers sending messages ("tell Alex...", "ask node-2 to..."),
  assigning tasks, checking who is online, and managing trust between peers.
  If the user refers to someone by name and wants to communicate with them,
  this is the skill to use.
compatibility: Requires clawsynapse CLI and a running clawsynapsed daemon
metadata:
  author: yuanjun5681
  version: "1.0"
allowed-tools:
  - "Bash(clawsynapse:*)"
---

# ClawSynapse

You have access to `clawsynapse`, a CLI tool for communicating with other AI agents on the ClawSynapse peer-to-peer network. The daemon (`clawsynapsed`) is already running on the local machine.

## When to Use This Skill

Use clawsynapse whenever the user wants to:
- Send a message to someone not in this conversation ("give Alex a message", "tell node-2 that...")
- Assign a task to another agent ("ask Alex to do xxx")
- Ask another agent a question ("check with Alex on the current status")
- Check who is online ("which nodes are available")

Node names (like "Alex", "node-2") correspond to peers on the network.

## First Step: Resolve the Target

If the user mentions a name but not a node ID, run:
```bash
clawsynapse --json peers
```
Match the name against node IDs in the result. If no match is found, ask the user to clarify.

## Examples

Here is how to translate common user requests into clawsynapse actions:

**User:** "给 Alex 发个消息，让他准备一下周会材料"
```bash
# Step 1: resolve "Alex" to a node ID
clawsynapse --json peers
# Step 2: send the message (assuming Alex's node ID is "alex")
clawsynapse publish --target alex --message "[request] Please prepare materials for the weekly meeting."
```

**User:** "问一下 node-2 现在进度怎么样"
```bash
clawsynapse request --target node-2 --message "[request] What is your current progress?" --timeout-ms 30000
```

**User:** "有哪些节点在线？"
```bash
clawsynapse peers
```

**User:** "告诉 Bob 任务完成了，结果是 42"
```bash
clawsynapse publish --target bob --message "[reply] Task completed. The result is 42."
```

## Incoming Message Format

When a peer sends you a message, it is delivered directly to you with a structured header:

```
[clawsynapse from=<senderNodeId> to=<localNodeId>]
<message body>
```

Example:

```
[clawsynapse from=node-2 to=node-1]
[request] What is the current system status?
```

**You do NOT need to poll or check the inbox** — messages are delivered to you automatically. When you see the `[clawsynapse ...]` header, you know:
- The message came from another agent on the ClawSynapse network
- `from=` tells you which node sent it — use this as the `--target` when replying
- `to=` is your own node ID

## Replying to Messages

**The sender is a remote agent on another machine — it CANNOT see your text output.** You MUST use `clawsynapse publish` to send any response. Never reply by simply outputting text.

Example — you receive:
```
[clawsynapse from=node-2 to=node-1]
[request] How many files are in the workspace?
```

❌ WRONG — node-2 will never see this:
```
There are 42 files in the workspace.
```

✅ CORRECT — this actually delivers your reply to node-2:
```bash
clawsynapse publish --target node-2 --message "[reply] There are 42 files in the workspace."
```

## Available Commands

### Messaging

```bash
# Publish a message to another agent (fire-and-forget, no reply expected)
clawsynapse publish --target <nodeId> --message "your message"

# Publish with session key (for conversation continuity)
clawsynapse publish --target <nodeId> --message "your message" --session-key "session-123"

# Publish with metadata
clawsynapse publish --target <nodeId> --message "your message" --metadata key1=value1 --metadata key2=value2

# Send a request and wait for reply (synchronous, blocks until reply or timeout)
clawsynapse request --target <nodeId> --message "your question"

# Send a request with custom timeout
clawsynapse request --target <nodeId> --message "your question" --timeout-ms 60000
```

### Network & Discovery

```bash
# List discovered peers
clawsynapse peers

# Get raw JSON output
clawsynapse --json peers

# Check daemon health and NATS connection status
clawsynapse health
```

### Trust Management

```bash
# View pending trust requests
clawsynapse trust pending

# Send a trust request to a peer
clawsynapse trust request --target <nodeId> --reason "collaboration on project X"

# Approve a trust request
clawsynapse trust approve --request-id <requestId>

# Reject a trust request
clawsynapse trust reject --request-id <requestId> --reason "unknown peer"

# Revoke trust for a peer
clawsynapse trust revoke --target <nodeId> --reason "no longer needed"
```

### Authentication

```bash
# Initiate authentication challenge with a peer
clawsynapse auth challenge --target <nodeId>
```

## Publish vs Request

- **`publish`** — Fire-and-forget. Sends a message to the target node's inbox. Does NOT wait for a reply. Use this for notifications, status updates, task results, and any one-way communication.
- **`request`** — Synchronous RPC. Sends a message and blocks until the target node replies (or timeout). Use this only when you need an immediate answer.

**In most cases, prefer `publish`.**

## Message Intent Tags

When sending messages, prefix the body with an intent tag so the receiving agent knows how to handle it:

| Tag | Usage |
|-----|-------|
| `[request]` | Asking the peer to do something or answer a question |
| `[reply]` | Responding to a previous `[request]` |
| `[notify]` | One-way notification, no response expected |
| `[data]` | Sending structured data or payload |
| `[end]` | Closing the conversation — do not reply to this |

Example:
```bash
clawsynapse publish --target node-2 --message "[request] Can you summarize the latest logs?"
```

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--api-addr` | `127.0.0.1:18080` | Local API address of clawsynapsed |
| `--timeout` | `5s` | CLI request timeout |
| `--json` | `false` | Output raw JSON response |

## Collaboration Rules

### Receiving Messages

1. **Messages arrive automatically** — You will receive messages with the `[clawsynapse ...]` header. Do NOT run `clawsynapse messages` to check inbox — that is only for manual inspection.
2. **Always reply via `clawsynapse publish`** — See "Replying to Messages" above. Never output text as a reply.
3. **Auto-handle when safe** — Simple queries, status checks, and public information can be answered directly via `publish` without asking the user.
4. **Notify user when needed** — The following scenarios require user confirmation:
   - Sending files or sensitive data to a peer
   - Modifying local files or configuration
   - Making decisions on behalf of the user
   - Accessing the user's private information

### Sending Messages

1. **User-initiated only** — Only **proactively** send messages (starting a new conversation) when the user explicitly asks. Do not autonomously contact other nodes. However, **replying to received messages** is NOT user-initiated — you should reply via `clawsynapse publish` as described in "Receiving Messages" above.
2. **Resolve peer first** — If the user does not specify a node ID, run `clawsynapse --json peers` to list discovered peers, then let the user choose or match by context.
3. **Keep messages concise** — One topic per message.
4. **Include context** — The receiving agent has no access to your conversation history. Provide enough background for the message to be self-contained.

### Conversation Lifecycle

1. **Start** — Before initiating, tell the user which peer you will contact and why.
2. **Progress** — If a collaboration exceeds 2 round-trips, give the user a progress update.
3. **Completion** — Judge by role:
   - **Initiator**: complete when the reply satisfies your original need.
   - **Responder**: complete when you have sent the requested information.
4. **Close** — Send a `[end]` message and stop. When you receive `[end]`, do not reply.
5. **Timeout** — If no reply within 60 seconds, inform the user and ask whether to retry.

### Trust Management

1. **Handshake requests** — Present the peer's info and reason to the user. Let the user decide.
2. **Never auto-approve** — Do not automatically approve any trust request.

## Important Notes

- Do NOT run `clawsynapsed` (the daemon) — it is managed separately.
- Peers must be discovered and trusted before messaging (unless trust mode is `open`).
- Use `--json` flag when you need to parse results programmatically.

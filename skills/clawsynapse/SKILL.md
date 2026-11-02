---
name: clawsynapse
description: >
  Use this skill when the user wants to contact, message, or collaborate with
  another person, agent, or bot — even if they don't mention "ClawSynapse"
  directly. Covers sending messages ("tell Alex...", "ask bot-2 to..."),
  assigning tasks, checking who is online, and managing trust between peers.
  If the user refers to someone by name and wants to communicate with them,
  this is the skill to use.
compatibility: Requires clawsynapse CLI access
metadata:
  author: yuanjun5681
  version: "2.0"
allowed-tools:
  - "Bash(clawsynapse:*)"
---

# ClawSynapse

You have access to `clawsynapse`, a CLI tool for communicating with independent Bots on the ClawSynapse peer-to-peer network. Local Bots explicitly use `role=console` (human inbox, no Harness) or `role=agent` (one Agent Harness); `device` is not available. A remote Bot's missing or unknown Role remains unknown and does not invalidate otherwise authorized communication.

## Response Contract

When handling ClawSynapse communication tasks, follow these response rules strictly:

- Do NOT restate payloads, clarifications, task summaries, or Todo plans in the chat UI.
- Outside of a send confirmation, do NOT output any business content in the chat UI.
- After a successful send, output exactly one line: `ACK <message_type>`.
- If waiting for a new user reply or a follow-up system message, output exactly: `WAITING`.
- If a command fails, output exactly `ERR <reason>` or `ERR publish failed: <code>`.
- Do NOT output extra explanation.
- Do NOT paste JSON.
- Do NOT repeat what was sent.

## When to Use This Skill

Use clawsynapse whenever the user wants to:
- Send a message to someone not in this conversation ("give Alex a message", "tell bot-2 that...")
- Assign a task to another agent ("ask Alex to do xxx")
- Ask another agent a question ("check with Alex on the current status")
- Check who is online ("which bots are available")

Bot names (like "Alex", "bot-2") correspond to peers on the network.

## Current CLI Scope

This skill is focused on peer communication workflows. The current `clawsynapse` CLI also includes other top-level commands such as `init`, `service`, `dashboard`, `logs`, `upgrade`, and `version`, but those are not the primary tools for this skill.

Messaging-related commands currently available in the CLI are:

- `health`
- `peers`
- `messages`
- `publish`
- `auth challenge`
- `trust pending|request|approve|reject|revoke`
- `transfer send|get|delete|list`
- `transfers`

Set `BOT_A` to the full local Bot ID before using these examples. Every business command requires an explicit `--bot "$BOT_A"`; the remote peer is passed separately with `--target`.

Bot creation is an admin operation: use `bot create --role console --name NAME`, or an explicit role-bearing JSON file for an Agent with a supported `harness.adapter`. Console configuration omits the entire `harness`, `maxConcurrent`, and `maxQueued` fields; Role cannot be switched through ordinary config updates. Full creation and offline schemaVersion 3 migration commands are documented in `docs/cli.md`.

Console chat and feedback are durable inbox data, not model runs. An execution request to a Console returns `console.execution_unsupported` (or `execution.expired`) without started/accepted/completed events. A successful publish only confirms durable transport acceptance; correlate the returned message ID with stored request/run events to determine execution outcome.

## First Step: Resolve the Target

If the user mentions a name but not a bot ID, run:
```bash
clawsynapse --bot "$BOT_A" --json peers
```
Match the name against bot IDs in the result. If no match is found, ask the user to clarify.

## Examples

Here is how to translate common user requests into clawsynapse actions:

**User:** "给 Alex 发个消息，让他准备一下周会材料"
```bash
# Step 1: resolve "Alex" to a bot ID
clawsynapse --bot "$BOT_A" --json peers
# Step 2: send the message (assuming Alex's bot ID is "alex")
# Use the global --json flag to capture the sessionKey for follow-up messages
clawsynapse --bot "$BOT_A" --json publish --target alex --message "[request] Please prepare materials for the weekly meeting."
# Capture the returned sessionKey and reuse it for all follow-up messages in the same task
```

**User:** "问一下 bot-2 现在进度怎么样"
```bash
# Start a normal conversation and keep the returned sessionKey
clawsynapse --bot "$BOT_A" --json publish --target bot-2 --message "[request] What is your current progress?"
```

**User:** "有哪些Bot在线？"
```bash
clawsynapse --bot "$BOT_A" peers
```

**User:** "告诉 Bob 任务完成了，结果是 42"
```bash
clawsynapse --bot "$BOT_A" publish --target bob --message "[reply] Task completed. The result is 42."
```

## Incoming Message Format

When a peer sends you a message, it is delivered directly to you with a structured header:

```
[clawsynapse from=<senderBotId> to=<localBotId> session=<sessionKey>]
<message body>
```

Example:

```
[clawsynapse from=bot-2 to=bot-1 session=task-abc]
[request] What is the current system status?
```

**For an Agent Bot, its Harness delivers messages automatically** — manual inbox polling is unnecessary for that execution path. A Console Bot instead keeps a durable inbox for its human client and does not invoke this skill. When you see the `[clawsynapse ...]` header in a configured Agent Harness:
- The sender is another Bot on the ClawSynapse network; its Role is not proof of authority or readiness
- `from=` tells you which bot sent it — use this as the `--target` when replying
- `to=` is your own bot ID
- `session=` identifies the conversation session — **when present, you MUST include `--session-key <sessionKey>` in your reply** to keep the task in the same conversation thread

## Replying to Messages

**The sender is a remote agent on another machine — it CANNOT see your text output.** You MUST use `clawsynapse --bot "$BOT_A" publish` to send any response. Never reply by simply outputting text.

Example — you receive:
```
[clawsynapse from=bot-2 to=bot-1 session=task-abc]
[request] How many files are in the workspace?
```

❌ WRONG — bot-2 will never see this:
```
There are 42 files in the workspace.
```

❌ WRONG — missing `--session-key`, breaks conversation continuity:
```bash
clawsynapse --bot "$BOT_A" publish --target bot-2 --message "[reply] There are 42 files in the workspace."
```

✅ CORRECT — includes `--session-key` to keep the same session:
```bash
clawsynapse --bot "$BOT_A" publish --target bot-2 --message "[reply] There are 42 files in the workspace." --session-key task-abc
```

## Commands Used By This Skill

### Messaging

```bash
# Inspect recent messages manually
clawsynapse --bot "$BOT_A" messages

# Publish a message to another agent
clawsynapse --bot "$BOT_A" publish --target <botId> --message "your message"

# Publish with a specific message type (default: chat.message)
clawsynapse --bot "$BOT_A" publish --target <botId> --type task.assign --message "your message"

# Publish with session key (for conversation continuity)
clawsynapse --bot "$BOT_A" publish --target <botId> --message "your message" --session-key "session-123"

# Publish with metadata
clawsynapse --bot "$BOT_A" publish --target <botId> --message "your message" --metadata key1=value1 --metadata key2=value2
```

### Network & Discovery

```bash
# List discovered peers
clawsynapse --bot "$BOT_A" peers

# Get raw JSON output
clawsynapse --bot "$BOT_A" --json peers

# Check local API and network status
clawsynapse --bot "$BOT_A" health
```

### Trust Management

```bash
# View pending trust requests
clawsynapse --bot "$BOT_A" trust pending

# Send a trust request to a peer
clawsynapse --bot "$BOT_A" trust request --target <botId> --reason "collaboration on project X"

# Send a trust request with repeatable capability flags
clawsynapse --bot "$BOT_A" trust request --target <botId> --capability chat --capability tools

# Approve a trust request
clawsynapse --bot "$BOT_A" trust approve --request-id <requestId>

# Reject a trust request
clawsynapse --bot "$BOT_A" trust reject --request-id <requestId> --reason "unknown peer"

# Revoke trust for a peer
clawsynapse --bot "$BOT_A" trust revoke --target <botId> --reason "no longer needed"
```

### Authentication

```bash
# The only auth subcommand currently exposed by the CLI
clawsynapse --bot "$BOT_A" auth challenge --target <botId>
```

### File Transfer

```bash
# Send a file to a peer
clawsynapse --bot "$BOT_A" transfer send --target <botId> --file /path/to/file

# Get transfer details
clawsynapse --bot "$BOT_A" transfer get --id <transferId>

# Delete a transfer record
clawsynapse --bot "$BOT_A" transfer delete --id <transferId>

# List transfers
clawsynapse --bot "$BOT_A" transfer list
clawsynapse --bot "$BOT_A" transfers
```

## OpenClaw Messaging Rule

- **OpenClaw agents MUST use `clawsynapse --bot "$BOT_A" publish` for peer-to-peer communication.**
- Rationale: this skill relies on session continuity through `sessionKey` and explicit `[reply]` / `[notify]` messages.
- If you need a follow-up, send another `publish` in the same `--session-key`.

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
clawsynapse --bot "$BOT_A" publish --target bot-2 --message "[request] Can you summarize the latest logs?"
```

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--api-addr` | `127.0.0.1:18080` | Local API address |
| `--bot` | none | Required local Bot ID or unambiguous prefix for business commands |
| `--timeout` | `5s` | Local API timeout |
| `--json` | `false` | Output raw JSON response |

## Collaboration Rules

### Receiving Messages

1. **Messages arrive automatically** — You will receive messages with the `[clawsynapse ...]` header. Do NOT run `clawsynapse --bot "$BOT_A" messages` to check inbox — that is only for manual inspection.
2. **Always reply via `clawsynapse --bot "$BOT_A" publish`** — See "Replying to Messages" above. Never output text as a reply. **If the incoming message has `session=`, always include `--session-key` in your reply.**
3. **Auto-handle when safe** — Simple queries, status checks, and public information can be answered directly via `publish` without asking the user.
4. **Notify user when needed** — The following scenarios require user confirmation:
   - Sending files or sensitive data to a peer
   - Modifying local files or configuration
   - Making decisions on behalf of the user
   - Accessing the user's private information
5. **Send exactly once per turn unless there is a real state change** — Do not send the same answer twice. Do not send both a content reply and a second "closing" message unless the conversation truly needs both.

### Sending Messages

1. **User-initiated only** — Only **proactively** send messages (starting a new conversation) when the user explicitly asks. Do not autonomously contact other bots. However, **replying to received messages** is NOT user-initiated — you should reply via `clawsynapse --bot "$BOT_A" publish` as described in "Receiving Messages" above.
2. **Resolve peer first** — If the user does not specify a bot ID, run `clawsynapse --bot "$BOT_A" --json peers` to list discovered peers, then let the user choose or match by context.
3. **Keep messages concise** — One topic per message.
4. **Include context** — The receiving agent has no access to your conversation history. Provide enough background for the message to be self-contained.
5. **Use `clawsynapse --bot "$BOT_A" publish` for business replies** — Do not place the actual reply content in the chat UI.

### Conversation Lifecycle

1. **Start** — Use `clawsynapse --bot "$BOT_A" --json publish` for the first message. The response contains a `sessionKey` — save it for all follow-up messages in this task.
2. **Continue** — All subsequent messages in the same task MUST include `--session-key <sessionKey>` to maintain conversation continuity.
3. **Respond** — When replying to a received message that has `session=`, always include `--session-key` with that value.
4. **Progress** — If a collaboration exceeds 2 round-trips, give the user a progress update.
5. **Completion** — Judge by role:
   - **Initiator**: complete when the reply satisfies your original need.
   - **Responder**: complete when you have sent the requested information.
6. **Close** — Do NOT automatically send `[end]` after every `[reply]`. Only send `[end]` when the user explicitly wants to close the thread or the remote side has asked to close it. When you receive `[end]`, do not reply.
7. **Timeout** — If no reply within 60 seconds, inform the user and ask whether to retry.

### Trust Management

1. **Handshake requests** — Present the peer's info and reason to the user. Let the user decide.
2. **Never auto-approve** — Do not automatically approve any trust request.

## Important Notes

- Use `clawsynapse` commands only in this skill. Do not tell the user to run or manage background services here.
- Peers must be discovered and trusted before messaging (unless trust mode is `open`).
- Use `--json` flag when you need to parse results programmatically.
- `auth` currently only supports `challenge` from the CLI.
- `trust request` supports repeatable `--capability` flags.
- Do NOT send fields that are not defined by the protocol or CLI.

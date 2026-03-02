<p align="center">
  <img src="assets/nanoclaw-logo.png" alt="NanoClaw" width="400">
</p>

<p align="center">
  A personal AI assistant that runs securely in Docker containers with peer-to-peer agent networking.
</p>

<p align="center">
  <a href="https://discord.gg/VGWXrf8x"><img src="https://img.shields.io/discord/1470188214710046894?label=Discord&logo=discord&v=2" alt="Discord"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README_CN.md">中文</a>
</p>

---

## What is NanoClaw?

NanoClaw is a lightweight, self-hosted AI assistant you can message via WhatsApp. Each conversation group runs in an isolated Docker container with its own filesystem and memory. Agents can communicate with other AI agents across the internet via [Pilot Protocol](https://pilotprotocol.network).

**Key features:**

- **WhatsApp I/O** — Message your AI assistant from your phone
- **Container isolation** — Each group runs in its own Docker sandbox
- **Pilot Protocol** — Agents discover and message other agents peer-to-peer
- **Scheduled tasks** — Recurring jobs that run autonomously
- **Agent Swarms** — Teams of agents collaborating on complex tasks
- **Per-group memory** — Isolated `CLAUDE.md` memory per conversation
- **Extensible via skills** — Add Gmail, Telegram, X/Twitter and more

## Prerequisites

| Dependency | Version | Purpose |
|------------|---------|---------|
| [Node.js](https://nodejs.org) | 20+ | Host process runtime |
| [Docker](https://docker.com/products/docker-desktop) | Latest | Container runtime for agents |
| [socat](https://linux.die.net/man/1/socat) | Any | Pilot Protocol socket bridge (macOS) |
| [Pilot Protocol](https://pilotprotocol.network/docs/getting-started) | Latest | Agent-to-agent networking (optional) |
| [Claude Code](https://claude.ai/download) | Latest | AI-native setup and customization |
| [Rust](https://rustup.rs) | Latest | Desktop app build (optional) |

### Install dependencies

**macOS (Homebrew):**

```bash
brew install node socat
# Docker Desktop: https://docker.com/products/docker-desktop
```

**Linux (apt):**

```bash
sudo apt install nodejs npm socat docker.io
```

**Pilot Protocol (optional):**

```bash
curl -fsSL https://raw.githubusercontent.com/TeoSlayer/pilotprotocol/main/install.sh | sh
pilotctl init --registry 34.71.57.205:9000 --beacon 34.71.57.205:9001
pilotctl daemon start --hostname my-agent
```

## Quick Start

```bash
git clone https://github.com/gavrielc/nanoclaw.git
cd nanoclaw
claude
```

Then run `/setup`. Claude Code handles the rest: dependencies, WhatsApp authentication, container build, and service configuration.

## Environment Variables

Create a `.env` file in the project root:

```bash
# Authentication (one of these is required)
ANTHROPIC_API_KEY=sk-ant-api03-...        # Pay-per-use API key
CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-...  # Claude subscription token

# Agno agent model (optional, defaults to Claude)
AGNO_MODEL_ID=claude-sonnet-4-5-20250514
AGNO_API_KEY=sk-ant-api03-...
AGNO_BASE_URL=https://api.anthropic.com
AGNO_TEMPERATURE=0.7
AGNO_MAX_TOKENS=4096

# App settings (optional)
ASSISTANT_NAME=Andy              # Trigger word (default: Andy)
CONTAINER_TIMEOUT=1800000        # Container timeout in ms (default: 30min)
MAX_CONCURRENT_CONTAINERS=5      # Parallel container limit
LOG_LEVEL=info                   # debug | info | warn | error
```

> Only auth variables and `AGNO_*` / `PILOT_BRIDGE_PORT` are passed into containers. Other env vars stay on the host.

## Build

```bash
# Install host dependencies
npm install

# Build the host process
npm run build

# Build the agent container image
./container-agno/build.sh
```

Verify the container:

```bash
docker run --rm --entrypoint pilotctl nanoclaw-agent-agno:latest --json context
```

## Run

**Development:**

```bash
npm run dev
```

**Production (macOS launchd):**

```bash
# Install as persistent service
cp launchd/com.nanoclaw.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.nanoclaw.plist

# Manage
launchctl unload ~/Library/LaunchAgents/com.nanoclaw.plist  # stop
launchctl kickstart -k gui/$(id -u)/com.nanoclaw            # restart
```

**Desktop app (optional):**

Requires [Rust](https://rustup.rs). First-time setup:

```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
cd desktop && npm install && cd ..
```

Development:

```bash
npm run desktop:dev
```

The first build compiles Rust dependencies and may take several minutes. Subsequent builds are incremental.

**Logs:**

```bash
tail -f logs/nanoclaw.log          # host logs
ls groups/*/logs/container-*.log   # per-container logs
```

## Architecture

```
WhatsApp ──► Node.js host ──► Docker container (Agno agent)
               │                    │
               ├── SQLite DB        ├── /workspace/group (isolated fs)
               ├── IPC watcher      ├── /workspace/ipc (file-based IPC)
               ├── Task scheduler   ├── pilotctl (Pilot Protocol CLI)
               └── socat bridge     └── socat ──► host daemon
                     │
                     ▼
               Pilot Protocol daemon ──► P2P overlay network
```

**Host process** (`src/index.ts`): Connects to WhatsApp, routes messages to containers, manages IPC and scheduling.

**Agent container** (`container-agno/`): Python + Agno framework. Each group gets its own isolated container with mounted workspace. Includes `pilotctl` for agent-to-agent communication.

**Pilot Protocol bridge**: On macOS, Docker runs in a Linux VM so Unix sockets can't cross the boundary. A `socat` TCP bridge (port 19191) relays between the host daemon socket and containers.

### Key Files

| File | Purpose |
|------|---------|
| `src/index.ts` | WhatsApp connection, message routing, IPC |
| `src/container-runner.ts` | Container lifecycle, mounts, Pilot bridge |
| `src/task-scheduler.ts` | Scheduled task execution |
| `src/db.ts` | SQLite operations |
| `container-agno/Dockerfile` | Agent image (Python + Agno + pilotctl) |
| `container-agno/agent-runner/` | Code that runs inside containers |
| `container-agno/skills/` | Skills synced into each agent session |
| `groups/*/CLAUDE.md` | Per-group persistent memory |

## Pilot Protocol

[Pilot Protocol](https://pilotprotocol.network) gives your agent a permanent address on a P2P encrypted network. Other agents can discover and message yours.

**Setup on host:**

```bash
pilotctl daemon start --hostname my-agent
pilotctl info    # show your address and peers
```

**Inside containers, agents can:**

```bash
pilotctl --json info                                    # node status
pilotctl send-message other-agent --data "hello"        # send message
pilotctl --json inbox                                   # check inbox
pilotctl send-file other-agent /workspace/group/report  # send file
pilotctl ping other-agent                               # test connectivity
pilotctl handshake other-agent "collaboration request"  # establish trust
```

The bridge is automatic — when `~/.pilot/config.json` exists on the host, `container-runner.ts` starts a socat bridge and passes `PILOT_BRIDGE_PORT` to containers. The container entrypoint creates a local socket so `pilotctl` works transparently.

## Usage

Talk to your assistant with the trigger word (default: `@Andy`):

```
@Andy send an overview of the sales pipeline every weekday morning at 9am
@Andy review the git history for the past week each Friday
@Andy message agent-alpha asking for the latest report
```

From the main channel, manage groups and tasks:

```
@Andy list all scheduled tasks across groups
@Andy pause the Monday briefing task
@Andy join the Family Chat group
```

## Customizing

Tell Claude Code what you want:

- "Change the trigger word to @Bob"
- "Make responses shorter and more direct"
- "Add a custom greeting when I say good morning"

Or run `/customize` for guided changes.

### Available Skills

| Skill | Purpose |
|-------|---------|
| `/setup` | First-time installation and configuration |
| `/customize` | Add channels, integrations, modify behavior |
| `/debug` | Container issues, logs, troubleshooting |
| `/add-telegram` | Add Telegram channel |
| `/add-gmail` | Gmail integration |
| `/x-integration` | X/Twitter integration |

## Contributing

**Don't add features. Add skills.**

Contribute a skill file (`.claude/skills/your-skill/SKILL.md`) that teaches Claude Code how to transform a NanoClaw installation. Users run `/your-skill` and get clean code tailored to their needs.

See [existing skills](.claude/skills/) for examples.

### Request for Skills

- `/add-slack` — Slack channel
- `/add-discord` — Discord channel
- `/setup-windows` — Windows via WSL2 + Docker

## FAQ

<details>
<summary><b>Why WhatsApp?</b></summary>
Because the author uses WhatsApp. Fork it and run a skill to switch. That's the whole point.
</details>

<details>
<summary><b>Can I run this on Linux?</b></summary>
Yes. Run <code>/setup</code> and Docker is configured automatically.
</details>

<details>
<summary><b>Is this secure?</b></summary>
Agents run in Docker containers with only explicitly mounted directories visible. See <a href="docs/SECURITY.md">docs/SECURITY.md</a> for the full security model.
</details>

<details>
<summary><b>How do I debug issues?</b></summary>
Run <code>claude</code>, then <code>/debug</code>. Or ask Claude directly: "Why isn't the scheduler running?"
</details>

<details>
<summary><b>Is Pilot Protocol required?</b></summary>
No. It's optional. Without it, your agent works normally but can't communicate with other agents on the network.
</details>

## Community

Questions? Ideas? [Join the Discord](https://discord.gg/VGWXrf8x).

## License

[MIT](LICENSE)

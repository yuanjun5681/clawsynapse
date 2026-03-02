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

NanoClaw is a lightweight, self-hosted AI assistant. Each conversation group runs in an isolated Docker container with its own filesystem and memory. Agents can discover and communicate with other AI agents across the internet via [Pilot Protocol](https://pilotprotocol.network).

**Key features:**

- **Container isolation** — Each group runs in its own Docker sandbox
- **Pilot Protocol** — Agents discover and message other agents peer-to-peer
- **Scheduled tasks** — Recurring jobs that run autonomously
- **Agent Swarms** — Teams of agents collaborating on complex tasks
- **Per-group memory** — Isolated `CLAUDE.md` memory per conversation
- **Desktop app** — Native macOS app with tray icon (Tauri + Svelte)
- **Extensible via skills** — Add Gmail, Telegram, X/Twitter and more

## Architecture

```
Channels ──► Node.js host ──► Docker container (Agno agent)
               │                    │
               ├── SQLite DB        ├── /workspace/group (isolated fs)
               ├── IPC watcher      ├── /workspace/ipc (file-based IPC)
               ├── Task scheduler   ├── pilotctl (Pilot Protocol CLI)
               └── socat bridge     └── socat ──► host daemon
                     │
                     ▼
               Pilot Protocol daemon ──► P2P overlay network
```

## Getting Started

### 1. Install Pilot Protocol

Pilot Protocol gives your agent a permanent address on a P2P encrypted network. See [Pilot Protocol docs](https://pilotprotocol.network/docs/) for details.

```bash
# Install pilotctl
curl -fsSL https://raw.githubusercontent.com/TeoSlayer/pilotprotocol/main/install.sh | sh

# Initialize with registry and beacon
pilotctl init --registry 220.168.146.21:8164 --beacon 220.168.146.21:8165

# Start the daemon
pilotctl daemon start --hostname my-agent
```

### 2. Install socat (socket bridge)

The socat bridge relays between the host Pilot daemon socket and Docker containers.

**macOS:**

```bash
brew install socat
```

**Linux:**

```bash
sudo apt install socat
```

### 3. Install Docker and build the agent image

Install [Docker Desktop](https://docker.com/products/docker-desktop), then build the agent container:

```bash
./container-agno/build.sh
```

Verify:

```bash
docker run --rm --entrypoint pilotctl nanoclaw-agent-agno:latest --json context
```

### 4. Install dependencies and start the host process

```bash
# Install Node.js (22 recommended)
# macOS: brew install node
# Linux: sudo apt install nodejs npm

npm install
npm run dev
```

For production, install as a launchd service (macOS):

```bash
cp launchd/com.nanoclaw.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.nanoclaw.plist
```

### 5. Configure environment variables

Create a `.env` file in the project root:

```bash
# Agno agent model (required)
AGNO_MODEL_ID=your-model-id
AGNO_API_KEY=your-api-key
AGNO_BASE_URL=https://your-provider-api-url
AGNO_TEMPERATURE=0.7
AGNO_MAX_TOKENS=4096

# LangSmith tracing (optional)
LANGSMITH_TRACING=false
LANGSMITH_API_KEY=your-langsmith-api-key
LANGSMITH_ENDPOINT=https://api.smith.langchain.com
LANGSMITH_PROJECT=nanoclaw-agno

# App settings (optional)
ASSISTANT_NAME=Andy              # Trigger word (default: Andy)
CONTAINER_TIMEOUT=1800000        # Container timeout in ms (default: 30min)
MAX_CONCURRENT_CONTAINERS=5      # Parallel container limit
LOG_LEVEL=info                   # debug | info | warn | error
```

> Only `AGNO_*`, `LANGSMITH_*`, and `PILOT_BRIDGE_PORT` are passed into containers. Other env vars stay on the host.

### 6. Desktop app (optional)

The desktop app provides a native macOS tray icon and local web UI. Requires [Rust](https://rustup.rs).

```bash
# Install Rust (first time only)
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh

# Install desktop dependencies
cd desktop && npm install && cd ..

# Run in development mode
npm run desktop:dev
```

The first build compiles Rust dependencies and may take several minutes. Subsequent builds are incremental.

## Usage

Talk to your assistant with the trigger word (default: `@Andy`):

```
@Andy send an overview of the sales pipeline every weekday morning at 9am
@Andy review the git history for the past week each Friday
@Andy message agent-alpha asking for the latest report
```

**Logs:**

```bash
tail -f logs/nanoclaw.log          # host logs
ls groups/*/logs/container-*.log   # per-container logs
```

## Customizing

Open [Claude Code](https://claude.ai/download) in the project directory and tell it what you want:

- "Change the trigger word to @Bob"
- "Add Telegram as a channel"
- "Make responses shorter and more direct"

Or run `/setup` for first-time configuration, `/customize` for guided changes, `/debug` for troubleshooting.

## Community

Questions? Ideas? [Join the Discord](https://discord.gg/VGWXrf8x).

## License

[MIT](LICENSE)

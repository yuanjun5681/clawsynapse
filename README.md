# ClawSynapse

Language: **English** | [简体中文](./README.zh-CN.md)

ClawSynapse is a local networking layer for multi-agent interoperability.
It runs as an independent Go daemon (`clawsynapsed`) on the same machine as the agent product, connects outward to NATS, and bridges inward to local agent APIs through adapters.

## What It Provides

- Cross-agent messaging over a shared NATS bus
- Peer discovery and node registry
- Authentication and trust workflow
- Signed message flow and replay protection
- Local HTTP API for integration with CLI/skills/tools

## Architecture

```text
Agent <-> Local ClawSynapse Daemon <-> NATS <-> Remote ClawSynapse Daemon <-> Remote Agent
```

## Quick Start

Requirements:

- A running NATS server

### 1. Start the Daemon

Download the `clawsynapsed` binary for your platform from [GitHub Releases](https://github.com/yuanjun5681/clawsynapse/releases), then start a node:

```bash
clawsynapsed --node-id node-alpha
```

Start with OpenClaw adapter:

```bash
clawsynapsed \
  --node-id node-alpha \
  --trust-mode open \
  --agent-adapter openclaw \
  --openclaw-agent-id main
```

Or configure via environment variables:

```bash
export NODE_ID=node-alpha
export TRUST_MODE=open
export AGENT_ADAPTER=openclaw
export OPENCLAW_AGENT_ID=main
clawsynapsed
```

Use `--check-config` to print the resolved configuration and exit:

```bash
clawsynapsed --node-id node-alpha --check-config
```

### 2. Install the CLI

Install the `clawsynapse` CLI tool for managing running nodes:

```bash
# One-line install from GitHub Release
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash

# Or install from local dist/ (after make dist)
./scripts/install.sh

# Uninstall
./scripts/install.sh --uninstall
```

### 3. Install the Agent Skill

Give the following prompt to your AI agent (e.g. OpenClaw / Claude Code) so it can automatically install the ClawSynapse skill:

```text
Install the ClawSynapse skill: fetch the SKILL.md from https://github.com/yuanjun5681/clawsynapse/blob/main/skills/clawsynapse/SKILL.md and install it. Once installed, follow the instructions in the skill to communicate with other agents on the ClawSynapse network.
```

Once installed, the agent will be able to send and receive messages, discover peers, and manage trust on the ClawSynapse network.

### 4. Manage Nodes with the CLI

```bash
# Check daemon health
clawsynapse health

# List discovered peers
clawsynapse peers

# Send a message to a remote node
clawsynapse publish --target node-beta --message "hello from alpha"

# Send a request and wait for a reply
clawsynapse request --target node-beta --message "ping" --timeout-ms 5000

# Authenticate a peer
clawsynapse auth challenge --target node-beta

# Trust workflow
clawsynapse trust request --target node-beta --reason "collaboration"
clawsynapse trust pending
clawsynapse trust approve --request-id <req-id>
clawsynapse trust reject --request-id <req-id>
clawsynapse trust revoke --target node-beta

# View recent messages
clawsynapse messages
```

Global flags: `--api-addr host:port`, `--timeout duration`, `--json` (raw JSON output).

## Configuration

Configuration precedence: `CLI flags > OS environment variables > project-root .env > ~/.clawsynapse/config.yaml > defaults`

Default main config file: `~/.clawsynapse/config.yaml`

The project-root `.env` file is loaded automatically for local development.

Starter templates are available at `config.example.yaml` and `.env.example`.

Common environment variables:

- `NATS_SERVERS` (comma-separated)
- `NODE_ID`
- `LOCAL_API_ADDR`
- `DATA_DIR`
- `IDENTITY_KEY_PATH`
- `IDENTITY_PUB_PATH`
- `HEARTBEAT_INTERVAL_MS`
- `ANNOUNCE_TTL_MS`
- `TRUST_MODE` (`open` | `tofu` | `explicit`)

## Documentation

- [Overview](./docs/overview.md)
- [Concepts](./docs/concepts.md)
- [Messaging](./docs/messaging.md)
- [Trust](./docs/trust.md)
- [Integration](./docs/integration.md)
- [CLI](./docs/cli.md)
- [Operations](./docs/operations.md)

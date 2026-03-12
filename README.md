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

- Go 1.25+
- A running NATS server

Run locally:

```bash
go run ./cmd/clawsynapsed --node-id node-alpha
```

Or via Make:

```bash
make run
```

Run tests:

```bash
go test ./...
```

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

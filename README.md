# ClawSynapse

Language: **English** | [简体中文](./README.zh-CN.md)

ClawSynapse is a local networking layer for multi-agent communication.
It runs a local daemon (`clawsynapsed`) on your machine, connects to NATS, and gives you a CLI (`clawsynapse`) to discover peers, send messages, and manage trust.

## Quick Start

One command installs the CLI and daemon:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash
```

The installer prompts for daemon settings in an interactive terminal when creating the configuration for the first time. It does not create a Bot or generate a Bot identity.

After installation, create and start a Bot explicitly:

```bash
clawsynapse daemon
clawsynapse bot create --file bot.json
clawsynapse bots
clawsynapse --bot <local-bot-id> bot start
clawsynapse --bot <local-bot-id> health
```

For example, `bot.json` can bind this Bot to an installed OpenCode executable:

```json
{
  "displayName": "assistant",
  "harness": {
    "adapter": "opencode",
    "timeout": "2m"
  }
}
```

Choose the Harness model and credentials for your environment. Each Bot has its own identity, trust state, sessions, and workspace. Business commands require `--bot`, even when the daemon has only one Bot.

## Install

Default install includes both the CLI and daemon:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash
```

Install only the CLI:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --cli
```

Install only the daemon:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --daemon
```

For non-interactive installs, pass the values explicitly:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --daemon --nats-servers nats://127.0.0.1:4222 --local-api-addr 127.0.0.1:18080
```

The installer uses `clawsynapse init` to create `~/.clawsynapse/config.json` with `schemaVersion: 2` on first daemon install. With `--data-dir DIR`, the default config path is `DIR/config.json`; `--config PATH` selects a separate path. Existing version 2 configs are kept unchanged and interactive installs skip their prompts. A daemon-only install uses a temporary matching CLI for initialization without installing it permanently.

The installer has no Bot creation, adapter, trust, or identity flags. Configure those settings in a Bot JSON file and create the Bot explicitly. Each Bot's `did:key` and subject-safe Bot ID are derived from its own Ed25519 identity key.

## Upgrade

### Migrate a legacy single-Bot install

The daemon installer refuses legacy `config.yaml`/`config.yml` and single-Bot identity or trust files before changing the installation. It does not overwrite, import, or generate a replacement identity for legacy state.

Stop the old daemon and back up the legacy configuration and all referenced state. Install only the CLI to obtain the migration command, then migrate explicitly while the daemon remains stopped:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --cli
clawsynapse service stop
clawsynapse migrate --legacy-config /absolute/path/to/config.yaml --data-dir /absolute/path/to/daemon-root
```

The target can be the legacy data directory for an in-place migration or a separate empty directory. After migration, rerun the daemon installer with `--config /absolute/path/to/daemon-root/config.json --data-dir /absolute/path/to/daemon-root`. Do not use `init --overwrite` as a migration shortcut.


Upgrade to the latest release by running the same installer again:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash
```

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --version v0.0.x
```

To install a prerelease build, pass the exact tag explicitly:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --version v0.0.4-rc.1
```

After upgrading, restart the daemon to make sure the new version is active:

```bash
clawsynapse version
clawsynapsed --version
clawsynapse service restart
clawsynapse daemon
clawsynapse bots
```

## Common Commands

Check status:

```bash
clawsynapse daemon
clawsynapse bots
clawsynapse --bot <local-bot-id> health
clawsynapse --bot <local-bot-id> peers
```

Open the terminal dashboard and logs:

```bash
clawsynapse --bot <local-bot-id> dashboard
clawsynapse logs
clawsynapse logs --follow
```

Recent daemon logs are written to `~/.clawsynapse/log/clawsynapsed.log` on new installs.

Send a message:

```bash
clawsynapse --bot <local-bot-id> publish --target <peer-bot-id> --message "hello from local Bot"
```

Start authentication and trust workflow:

```bash
clawsynapse --bot <local-bot-id> auth challenge --target <peer-bot-id>
clawsynapse --bot <local-bot-id> trust request --target <peer-bot-id> --reason "collaboration"
clawsynapse --bot <local-bot-id> trust pending
clawsynapse --bot <local-bot-id> trust approve --request-id <req-id>
```

Read recent messages:

```bash
clawsynapse --bot <local-bot-id> messages
```

## Configuration

Main config file:

```text
~/.clawsynapse/config.json
```

Create a daemon config, or explicitly replace an existing version 2 config after backing it up:

```bash
clawsynapse init
clawsynapse init --overwrite --nats-servers nats://127.0.0.1:4222
clawsynapse service restart
```

If you only want to inspect the resolved daemon config:

```bash
clawsynapsed --check-config
```

`init` configures only the daemon: listener, shared NATS connection, logging, and daemon state directory. Bot Harness bindings and trust settings belong to each Bot's configuration, managed with `clawsynapse --bot <local-bot-id> bot config`.

The default daemon administrator credential is `~/.clawsynapse/admin.token`. For another daemon endpoint, use `clawsynapse --api-addr http://HOST:PORT --token-file /path/to/admin.token ...`. Bot-scoped credentials are separate; they cannot manage the daemon or access another Bot.

Core real end-to-end behavior has been verified with two independent OpenCode GLM-5.3-Flash bindings and an external peer on NATS 2.11.9, including crash recovery, Bot isolation, and shared-file retention. This is not exhaustive coverage of every platform, Harness, or fault boundary.

## Uninstall

Remove CLI and daemon:

```bash
./scripts/install.sh --uninstall
```

Remove only the daemon:

```bash
./scripts/install.sh --daemon --uninstall
```

Remove everything, including local state:

```bash
./scripts/install.sh --all --uninstall --purge
```

## More Docs

- [Overview](./docs/overview.md)
- [CLI](./docs/cli.md)
- [Operations](./docs/operations.md)
- [Messaging](./docs/messaging.md)
- [Trust](./docs/trust.md)
- [Integration](./docs/integration.md)

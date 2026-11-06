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
  "role": "agent",
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

Binaries default to `~/.local/bin`; creating and writing this user-level directory does not request sudo. Configuration and runtime data remain in `~/.clawsynapse/`. Use `--install-dir DIR` for another location; an explicitly selected protected directory may require sudo. Linux still requires sudo to register the current system-level systemd service; macOS uses a user LaunchAgent.

If the binary directory is not on PATH, the installer prints a shell-specific command without modifying startup files. Uninstall without `--install-dir` reads the recorded directory from `~/.clawsynapse/manifest.json`; an explicit directory overrides it. On Linux, reading the manifest requires `jq` or `python3`; otherwise specify the actual directory explicitly. If no manifest exists, uninstall targets `~/.local/bin`. Existing installs are not automatically moved from another directory.

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

Every install mode also installs the `clawsynapse` and `clawsynapse-context` Skills:

```text
~/.agents/skills/clawsynapse/SKILL.md
~/.agents/skills/clawsynapse-context/SKILL.md
```

Current OpenCode and OpenClaw support this user-level directory. For other Harnesses, use `--skills-dir DIR` or configure discovery manually. Pass `--no-skills` to skip. Reinstallation updates both `SKILL.md` files; keep customizations separately. CLI/daemon uninstallation preserves Skills.

Remote installs fetch Skills from the matching release tag, not `main`; `latest` uses the newly installed binary's version. Local `dist/` installs from a source checkout use that checkout's Skills. Existing Agent sessions may need to be reopened to discover newly installed Skills.

For non-interactive installs, pass the values explicitly:

```bash
curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
  bash -s -- --daemon --nats-servers nats://127.0.0.1:4222 --local-api-addr 127.0.0.1:18080
```

The installer uses `clawsynapse init` to create `~/.clawsynapse/config.json` with `schemaVersion: 3` on first daemon install. With `--data-dir DIR`, the default config path is `DIR/config.json`; `--config PATH` selects a separate path. Existing configs are not implicitly migrated: version 2 data requires an explicit offline layout upgrade before starting the new daemon. A daemon-only install uses a temporary matching CLI for initialization without installing it permanently.

The installer has no Bot creation, adapter, trust, or identity flags. Configure those settings in a Bot JSON file and create the Bot explicitly. Each Bot's `did:key` and subject-safe Bot ID are derived from its own Ed25519 identity key.

## Upgrade

### Migrate a version 2 multi-Bot layout

Stop the daemon and back up its complete DataDir before using the new CLI:

```bash
clawsynapse service stop
clawsynapse migrate --data-dir /absolute/path/to/daemon-root
```

This preserves identities, trust, journal cursors, Agent bindings and sessions. It converts Console Bots to `role=console` without a Harness. If interrupted, run `clawsynapse migrate --recover --data-dir /absolute/path/to/daemon-root` before restarting. Version 2 export bundles must first be converted with `clawsynapse bot convert --file /path/to/old-bundle --output /path/to/new-bundle`; the original bundle is retained.


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

Create a daemon config, or explicitly replace an existing version 3 config after backing it up (not a layout migration):

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

Daemon management is loopback-only and needs no token: CLI and desktop clients can create and manage Bots directly. Any local process can omit its token to obtain full management access; do not expose or forward this API to untrusted callers. `--token-file /path/to/bots/<botId>/credentials/api.token` is optional for Bot-scoped calls with a full `--bot` ID; explicitly supplied credentials retain scope restrictions and invalid tokens fail, but they are not local-process isolation. Remote endpoints, including HTTPS, are rejected. Cross-device Bots communicate through NATS; their identity, signatures, authentication and trust checks are unchanged.

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

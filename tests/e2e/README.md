# Real multi-Bot acceptance

The runner starts two **host daemons** against a uniquely named Docker Compose
project containing `nats:2.11.9` with JetStream. Bot A and Bot B use independent
real OpenCode bindings and private `--standalone` engines. External Bot R runs in
the other daemon using the built-in adapter; R is a network peer, not a claimed
real Harness. There are no simulated model executables or daemon containers.

## Prerequisites and invocation

- Docker with Compose; Python 3.9+; prebuilt `clawsynapsed` and `clawsynapse`.
- A real installed `opencode` advertising `run --standalone`, and access to the
  selected provider/model. The default is `zai-coding-plan/glm-5.3-flash`.
- The model must be able to execute the narrowly scoped workspace commands in
  the test prompts. The runner does not supply `--auto`, approve provider safety
  checks, alter account permissions, or install credentials.

```bash
python3 tests/e2e/runner.py \
  --daemon-bin /absolute/path/to/clawsynapsed \
  --cli-bin /absolute/path/to/clawsynapse \
  --opencode-bin /absolute/path/to/opencode
```

Binary defaults can also be set using `CLAWSYNAPSED_BIN`, `CLAWSYNAPSE_BIN`, and
`OPENCODE_BIN`. `--model` or `E2E_OPENCODE_MODEL` selects another real model.
`--turn-timeout` defaults to 150 seconds; `--deadline` bounds the entire scenario
at 1,200 seconds. The runner does not build binaries or run unit tests.

## Exercised assertions

- Independent A/B IDs, DIDs and scoped credentials; explicit CLI Bot selection;
  anonymous, cross-Bot, management and host-import authorization checks.
- Real signed discovery, challenge authentication, and manually approved trust
  with R. A's trust and alias for R do not appear in B.
- Real A/B model turns, native session and working-directory isolation, routed
  wire-v2 replies, large-integer/Unicode preservation, ordered execution events.
- One active plus two queued real commands is enforced for B; excess work stays
  on the durable broker until capacity frees, without concurrent model turns.
- Stopping A while B's actual workspace command is active does not interrupt B;
  restarting A retains its own session, alias and message replay cursor.
- A binding change creates a new workspace/session namespace; configuration
  updates use revision compare-and-swap.
- Killing the daemon and its owned native children after a real admitted command
  produces uncertain recovery without blindly repeating the workspace write.
- Two received files share one immutable entity but retain independent grants
  and workspace copies; deleting A's grant does not revoke B's; an archived B
  retains authorized reads and protects its content across daemon restart.

These are assertions implemented by the runner, **not a claim that acceptance has
passed**. Real provider behavior, latency, authentication, permissions and model
tool execution are prerequisites; failures remain failures rather than switching
to a fixture.

## Isolation, evidence, and cleanup

Each invocation owns a fresh canonical temporary root, ephemeral loopback ports,
its own Compose project, host process trees and JetStream volume. No existing
daemon, local repository workspace or fixed `tests/e2e/runtime` is used. NATS is
plain TCP inside this local-only fixture; this scenario does not claim mTLS
coverage.

Success writes `report.json` with Bot identities, native session IDs, file
references and passed assertions. `--keep` preserves that root and the private
broker for inspection and prints its exact cleanup command. Host daemons always
stop. Without `--keep`, only the invocation's own root, container and volume are
removed. Failure prints the last 100 lines of each owned daemon's log.

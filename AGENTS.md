# AGENTS.md

Guidance for coding agents working in `nanoclaw`.

## Scope and intent

- This is a small, single-process Node.js app with containerized agent execution.
- Prioritize minimal, understandable changes over abstractions.
- Keep security boundaries explicit (group isolation, mount restrictions, IPC auth checks).

## Rule sources checked

- `.cursor/rules/`: not present.
- `.cursorrules`: not present.
- `.github/copilot-instructions.md`: not present.
- Repository-level guidance comes from `CLAUDE.md`, `README.md`, and `docs/REQUIREMENTS.md`.

## Runtime and tooling snapshot

- Node.js: `>=20` (from `package.json`).
- TypeScript: strict mode enabled.
- Module system: ESM (`"type": "module"`, `NodeNext`).
- Formatter: Prettier with single quotes.
- Linter: none configured currently.
- Test framework: none configured currently.

## High-value directories

- `src/`: host runtime (routing, scheduler, DB, container orchestration).
- `container-agno/agent-runner/`: Python agent runner code that executes inside container image.
- `container-agno/skills/`: skills copied into each group session.
- `groups/`: per-group files and memory (`CLAUDE.md`).
- `data/`: runtime state (IPC, sessions, env bridge files).
- `store/messages.db`: SQLite database.

## Build, format, typecheck, run

Run from repository root unless noted.

### Main app (host)

- Install deps: `npm install`
- Dev server: `npm run dev`
- Build TS: `npm run build`
- Run built app: `npm run start`
- Type check only: `npm run typecheck`
- Format: `npm run format`
- Format check (CI-friendly): `npm run format:check`

### Container image (Agno agent)

- Build image: `./container-agno/build.sh`
- Rebuild without cache: `./container-agno/build.sh --no-cache`
- Verify image: `docker run --rm --entrypoint wc nanoclaw-agent-agno:latest -l /app/src/index.ts`

## Lint and test status (important)

- There is currently **no ESLint config** and **no test script** in root `package.json`.
- Do not claim lint/test pass unless you actually add and run those tools.
- Minimum validation for most changes today is:
  - `npm run typecheck`
  - `npm run build`
  - `npm run format:check`

## Running a single test (current reality)

- There is no first-class test harness in this repo yet, so there is no canonical single-test command.
- If you add Node/TS tests as part of your change, prefer documenting one of these patterns in your PR:
  - Node built-in tests (JS): `node --test path/to/file.test.js`
  - TS via tsx (if adopted): `npx tsx --test path/to/file.test.ts`
- If you introduce a framework (Vitest/Jest), also add scripts so single-test execution is explicit.

## Code style and conventions

### Imports and modules

- Use ESM imports only.
- Use `.js` extension in relative imports from TS files (NodeNext style), e.g. `./db.js`.
- Keep import groups in this order with a blank line between groups:
  1. Node built-ins
  2. third-party packages
  3. local modules
- Prefer named imports unless a package idiomatically uses default imports.

### Formatting

- Use Prettier defaults plus single quotes.
- Indentation is 2 spaces; avoid manual alignment formatting.
- Keep lines readable; rely on Prettier wrapping instead of hand-tuned wrap styles.
- Use trailing commas where Prettier inserts them.

### Types and TypeScript

- `strict` mode is on; keep code type-safe without disabling checks.
- Avoid `any`; prefer `unknown` with narrowing when needed.
- Add explicit return types on exported functions and public class methods.
- Use `interface` for object contracts used across modules.
- Use union literals for finite state (`'success' | 'error'`, etc.).
- Prefer `Record<string, T>` for dictionary-like maps.

### Naming

- `camelCase`: variables, functions, methods.
- `PascalCase`: classes, interfaces, type-like entities.
- `UPPER_SNAKE_CASE`: shared constants and env-derived config constants.
- Preserve existing DB/wire naming when already snake_case (`chat_jid`, `group_folder`).
- Use descriptive verb-first function names (`runTask`, `writeTasksSnapshot`, `setRouterState`).

### Error handling and logging

- Wrap I/O and process boundaries in `try/catch`.
- On caught errors, log with context objects using `logger.*` (pino), not bare strings.
- In long-lived loops (scheduler, IPC watcher), catch errors and continue looping.
- Prefer graceful degradation over process crash unless startup invariants fail.
- Use structured error returns at boundaries (`{ status: 'error', error: ... }`) where patterns already exist.
- Clear timers/resources in `finally` or equivalent completion paths.

### Filesystem, IPC, and DB patterns

- Use atomic writes for IPC files (`.tmp` then `rename`).
- Ensure parent directories exist with `fs.mkdirSync(..., { recursive: true })`.
- Keep per-group isolation assumptions intact (no cross-group IPC shortcuts).
- Validate authorization at host boundary (main vs non-main behavior).
- For SQLite changes, keep backward compatibility via migrations when possible.

### Security-sensitive practices

- Never broaden mounts without allowlist validation (`mount-security.ts`).
- Do not expose full `.env` content to containers; keep allowlisted variables only.
- Sanitize externally-derived folder names and IDs before filesystem use.
- Maintain least-privilege behavior for non-main groups.

## Change strategy for agents

- Prefer small, local edits in existing files over introducing new layers.
- Follow existing architecture: single process, explicit state, clear control flow.
- Keep comments minimal and only for non-obvious constraints.
- Do not refactor unrelated code while implementing targeted fixes.

## Recommended validation checklist per change

- Run `npm run typecheck`.
- Run `npm run build`.
- Run `npm run format:check`.
- If container behavior changed, rebuild image and perform a quick smoke test.
- If scheduler/IPC behavior changed, verify no group-isolation regressions.

## Commit/PR note quality

- Explain why the change is needed, not only what changed.
- Call out any security-impacting behavior changes explicitly.
- If tests are absent, state what manual/command validation was performed.

# AGENTS.md

This file defines practical rules for agentic coding agents working in this repository.
It is based on the current Go codebase, Makefile, and docs as of 2026-03-12.

## 1) Project Snapshot

- Language: Go (`go 1.25.1` in `go.mod`).
- Main module: `clawsynapse`.
- Entrypoint: `cmd/clawsynapsed/main.go`.
- Core packages live under `internal/`.
- Shared types live under `pkg/types/`.
- Build/test helper: `Makefile`.

## 2) Canonical Commands

Use these from repo root.

### Run daemon

```bash
go run ./cmd/clawsynapsed --node-id node-alpha
```

or

```bash
make run
```

### Build

```bash
go build ./...
```

Build the daemon binary only:

```bash
go build ./cmd/clawsynapsed
```

### Test (all)

```bash
go test ./...
```

or

```bash
make test
```

### Test (single package)

```bash
go test ./internal/auth
```

### Test (single test function)  <-- preferred for quick iteration

```bash
go test ./internal/auth -run '^TestHandleChallengeAckRejectsInvalidProof$' -count=1 -v
```

Generic pattern:

```bash
go test ./<package-path> -run '^TestName$' -count=1 -v
```

### Test by name across all packages

```bash
go test ./... -run '^TestValidateMessageModuleMismatch$' -count=1 -v
```

### Benchmarks (if added)

```bash
go test ./... -bench . -run '^$'
```

### Lint/format (no dedicated linter config currently)

Use standard Go tooling:

```bash
gofmt -w .
go vet ./...
```

Optional if available in environment:

```bash
golangci-lint run
```

## 3) Codebase Conventions

These conventions are inferred from existing code and should be preserved.

### Imports

- Group imports with a blank line between stdlib and non-stdlib.
- Keep module-local imports using `clawsynapse/...` paths.
- Avoid unused imports; keep import blocks gofmt-clean.

### Formatting

- Follow `gofmt` output exactly (tabs, spacing, wrapping).
- Keep functions focused; split helper logic into private methods.
- Prefer early returns to reduce nesting.

### Types and Structs

- Exported identifiers: `PascalCase`; internal/private: `camelCase`.
- Use explicit structs for protocol/API payloads.
- JSON tags use `lowerCamelCase` keys (for example `nodeId`, `requestId`).
- Use `omitempty` only for truly optional response/request fields.
- Prefer concrete types over `any`; use `map[string]any` only for flexible metadata.

### Naming

- Keep acronyms consistent with existing code: `ID`, `TS`, `TTL`, `API`.
- Keep message and error codes machine-oriented and dot-separated:
  - `auth.challenge.request`
  - `trust.already_pending`
  - `protocol.module_mismatch`
- For status values, reuse constants in `pkg/types/state.go`.

### Error Handling

- Validate inputs at function start and return immediately on invalid arguments.
- For local validation failures, use `errors.New("...")` with clear, actionable text.
- When propagating upstream errors across boundaries, wrap with context using `%w`.
- Use typed protocol errors (`protocol.NewError`) when caller needs stable error codes.
- Do not `panic` for expected runtime errors.

### Logging

- Use structured logs via `log/slog`.
- Keep log message text short and stable.
- Put variable data in structured fields (`slog.String`, etc.).
- Prefer `Warn` for recoverable message-validation or network issues.

### Concurrency and State

- Protect shared mutable state with `sync.Mutex` / `sync.RWMutex`.
- Keep critical sections small.
- Return copies when exposing internal slices/maps to callers.
- For long-running loops, support context cancellation and stop tickers.

### Context and Time

- Thread `context.Context` through boundaries that can block.
- Use explicit timeouts for network/API operations.
- Store timestamps in Unix milliseconds (`time.Now().UnixMilli()`).

### Security and Trust Logic

- Preserve signature verification and replay protection flows.
- Never bypass trust/auth status checks except in explicit `open` mode branches.
- Keep key handling strict (path checks, key size checks, file permissions).

### API Layer

- Return JSON using the `types.APIResult` shape.
- Keep response `code` values stable for automation.
- Include `ts` in responses.
- Use HTTP 400 for invalid client payloads; 200 for successful state transitions.

### Tests

- Name tests as `Test<Behavior>`.
- Keep assertions explicit with `t.Fatal` / `t.Fatalf`.
- Use helpers with `t.Helper()` where appropriate.
- Prefer deterministic tests; avoid sleeping where possible.
- For temporary files/dirs, use `t.TempDir()`.

## 4) Repository Layout Guidance

- `cmd/`: process entrypoints only.
- `internal/`: implementation details by domain (`auth`, `trust`, `messaging`, etc.).
- `pkg/types/`: externally sharable data types/constants.
- `docs/`: design and operational documentation.

## 5) Rule Files Check (Cursor/Copilot)

Checked paths:

- `.cursorrules`
- `.cursor/rules/**`
- `.github/copilot-instructions.md`

Current result:

- No Cursor rule files found.
- No Copilot instruction file found.

If these files are added later, treat them as higher-priority local instructions and update this AGENTS.md accordingly.

## 6) Agent Workflow Recommendation

When making changes, default flow:

1. Read target package and nearby tests.
2. Make minimal, scoped edits aligned with existing patterns.
3. Run `gofmt` on changed files.
4. Run package tests first, then `go test ./...` if feasible.
5. Keep commits focused and message/error codes stable.

# AGENTS.md

Practical instructions for agentic coding agents working in this repository.
Reviewed against the current Go codebase, `Makefile`, and rule-file locations on 2026-03-12.

## 1) Project Snapshot
- Language: Go (`go 1.25.1` in `go.mod`).
- Module path: `clawsynapse`.
- Main entrypoint: `cmd/clawsynapsed/main.go`.
- Main implementation lives under `internal/`.
- Shared exported data types live under `pkg/types/`.
- Current helper targets in `Makefile`: `run`, `test`, `test-unit`.

## 2) Canonical Commands
Run all commands from the repository root.

### Run
Run the daemon:
```bash
go run ./cmd/clawsynapsed --node-id node-alpha
```
Equivalent helper target:
```bash
make run
```

### Build
Build everything:
```bash
go build ./...
```
Build only the daemon binary:
```bash
go build ./cmd/clawsynapsed
```

### Test
Run the full test suite:
```bash
go test ./...
```
Equivalent helper target:
```bash
make test
```
Run a single package:
```bash
go test ./internal/auth
```
Run a single test function in a package (preferred for fast iteration):
```bash
go test ./internal/auth -run '^TestHandleChallengeAckRejectsInvalidProof$' -count=1 -v
```
Reusable pattern:
```bash
go test ./<package-path> -run '^TestName$' -count=1 -v
```
Run one test name across all packages:
```bash
go test ./... -run '^TestValidateMessageModuleMismatch$' -count=1 -v
```
Run benchmarks if they exist:
```bash
go test ./... -bench . -run '^$'
```

### Format and lint
There is no dedicated lint config checked in right now. Use standard Go tooling:
```bash
gofmt -w .
go vet ./...
```
Optional, only if available in the environment:
```bash
golangci-lint run
```

## 3) Repository Layout Guidance
- `cmd/`: process entrypoints only.
- `internal/api`: HTTP handlers and API response wiring.
- `internal/auth`, `internal/trust`, `internal/messaging`, `internal/discovery`: domain services.
- `internal/protocol`: message schemas, subject validation, protocol errors, signatures.
- `internal/store`: local persistence and atomic file writes.
- `internal/config`: CLI and environment-driven configuration loading.
- `pkg/types`: shared states and API result types.
- `docs/`: design and operational notes.

## 4) Code Style and Conventions
These conventions are inferred from the current code and should be preserved.

### Imports
- Group imports with a blank line between stdlib and non-stdlib imports.
- Keep local imports on `clawsynapse/...` paths.
- Do not leave unused imports; keep blocks `gofmt`-clean.

### Formatting and structure
- Follow `gofmt` output exactly; do not hand-format around it.
- Prefer small, focused functions and private helpers over deeply nested logic.
- Prefer early returns for validation and error cases.
- Keep control-flow straightforward; avoid unnecessary abstraction.

### Types and data modeling
- Exported names use `PascalCase`; internal names use `camelCase`.
- Prefer explicit structs for protocol payloads and API request bodies.
- JSON tags use `lowerCamelCase` keys such as `nodeId`, `requestId`, `atMs`.
- Use `omitempty` only when a field is genuinely optional.
- Prefer concrete types over `any`; use `map[string]any` only for flexible metadata or API payload envelopes.
- Reuse shared status constants from `pkg/types/state.go` instead of duplicating strings.

### Naming
- Keep acronyms consistent with the existing code: `ID`, `TS`, `TTL`, `API`.
- Message types and protocol codes are machine-oriented, lowercase, and dot-separated.
- Existing examples to follow: `auth.challenge.request`, `trust.already_pending`, `protocol.module_mismatch`.
- Keep public config field names aligned with current JSON/flag vocabulary.

### Error handling
- Validate inputs at the start of a function and fail fast.
- Use `errors.New("...")` for simple local validation errors.
- Wrap propagated errors with context using `%w` when crossing boundaries.
- Use typed protocol errors via `protocol.NewError` when callers depend on stable error codes.
- Do not `panic` for expected runtime failures.

### Logging
- Use structured logging through `log/slog`.
- Keep log messages short, stable, and easy to grep.
- Put dynamic values in structured fields such as `slog.String(...)`.
- Prefer `Warn` for recoverable validation, replay, or network issues.

### Concurrency and state
- Protect shared mutable state with `sync.Mutex` or `sync.RWMutex`.
- Keep lock scopes tight.
- Return copies when exposing internal slices or maps.
- For long-running loops or subscriptions, support context cancellation and stop timers/tickers.

### Time, security, and persistence
- Use `time.Now().UnixMilli()` for persisted or protocol timestamps.
- Apply explicit timeouts to network and API operations.
- Preserve signature verification, replay protection, and trust-mode checks.
- Do not weaken key handling; keep path validation, key-size checks, and restrictive file permissions intact.
- When writing local state, preserve atomic-write behavior and current file modes.

### API layer
- HTTP responses should use the `types.APIResult` shape.
- Keep response `code` values stable for automation.
- Include `ts` in API responses.
- Use HTTP 400 for invalid client payloads and HTTP 200 for successful state transitions.

## 5) Testing Conventions
- Test names follow `Test<Behavior>`.
- Prefer explicit assertions with `t.Fatal` and `t.Fatalf`.
- Use helper functions with `t.Helper()` when extracting setup/assertion utilities.
- Keep tests deterministic; avoid sleeps unless there is no stronger option.
- Use `t.TempDir()` for temporary files and directories.
- When iterating on a bug fix, run the narrowest relevant single test first, then the package, then `go test ./...` if practical.

## 6) Rule Files Check
Checked locations:
- `.cursorrules`
- `.cursor/rules/**`
- `.github/copilot-instructions.md`

Current result:
- No Cursor rule files were found.
- No Copilot instruction file was found.

If any of these files are added later, treat them as higher-priority repository instructions and update this file accordingly.

## 7) Recommended Agent Workflow
1. Read the target package and nearby tests before editing.
2. Make the smallest change that matches existing patterns.
3. Run `gofmt` on changed files.
4. Run the most targeted relevant test command first.
5. Expand to package tests, then `go test ./...` when feasible.
6. Keep commits focused, and do not change stable protocol/error codes unless the task requires it.

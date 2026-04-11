# 🤖 Agent Instructions: Project Dojo

## 1. System Prompt & Role
You are an expert Go (Golang) systems engineer and architect. Your task is to build **Project Dojo**, a high-performance, deterministic Black-Box Testing Orchestrator and Service Virtualization Engine.

You act autonomously but incrementally. You must follow **Strict Test-Driven Development (TDD)**. Never implement a feature without first writing a failing Go test that proves the feature does not yet exist or is incorrect.

## 2. Project Context: What is Dojo?
Dojo is a testing engine that executes `.plan` files. It evaluates a SUT (Software Under Test) by wrapping it in a transparent proxy environment.
* **The Initiator:** Proactively sends requests to the SUT (e.g., an HTTP webhook).
* **The Observer:** Intercepts outbound SUT traffic (HTTP, Postgres, etc.) and matches it against expectations in the `.plan` file.
* **Startup Plan:** A `startup.plan` file in the suite directory asserts on outbound traffic the SUT emits **after** its HTTP listener is ready and **before** any test in `RunSuite` runs. It may contain **only** `Expect` lines (no `Perform`). If startup expectations fail or time out, `StartProxies` returns an error and **no tests run**. See [docs/startup-plan.md](docs/startup-plan.md) and the example suite’s [example/tests/blackbox/startup.plan](example/tests/blackbox/startup.plan).
* **Configuration:** Technical details (URLs, timeouts) live in `dojo.yaml`. The DSL (`test.plan`) remains purely logical.
* **Matching:** Outbound traffic is tied to the active test by **normalized full equality** between the resolved `expected_request` fixture and the actual payload (SQL: collapse whitespace and strip trailing `;`; HTTP: canonical JSON when valid). There is no separate correlation block for routing. **Each pair of tests in a suite must not share the same normalized expected request for the same API name**; the workspace loader errors on duplicates.
* **Concurrent outbound calls:** [`ProcessRequest`](internal/engine/match.go) and [`ProcessResponse`](internal/engine/match.go) share a mutex so ordered `Expect` lines stay deterministic when the SUT issues parallel HTTP (or completes live expectations) against the same mock API. Suite authors should still use **tight** JSON subset fixtures so unrelated background traffic cannot satisfy the wrong `Expect` (see [docs/dojo-skill.md](docs/dojo-skill.md)).
* **Timeouts:** `dojo.yaml` timeouts mirror the DSL: `perform` (trigger call, default 5s) and `expect` (wait for outbound traffic, default 2s). Per-API `timeout` in `dojo.yaml` overrides `expect` for that API. For real LLM suites, set `timeout: "30s"` (or higher) on the LLM API config.

## 3. Architectural Interfaces
Design the system around these core interfaces. Do not deviate without explicit permission.

```go
// Adapter represents an external protocol (HTTP, Postgres, AMQP).
type Adapter interface {
    // Trigger is used by the Initiator to start a test.
    Trigger(ctx context.Context, payload []byte, endpointConfig map[string]any) error
    
    // Listen is used by the Observer to intercept and match SUT traffic.
    Listen(ctx context.Context, matchTable MatchTable) error
}

// Evaluator evaluates a payload against an expected state or AI rule.
type Evaluator interface {
    // Evaluate compares actual data against an expected rule.
    Evaluate(ctx context.Context, actual []byte, expectedRule string) (EvaluatorResult, error)
}
```

## 4. Commenting & Documentation (Go Doc Standard)
You must follow the official **Go Doc** format for all exported identifiers:
* **Name-First:** Start every doc comment with the name of the item (e.g., `// Adapter represents...`).
* **Complete Sentences:** Use proper grammar, capitalization, and end with a period.
* **Explain the "Why":** Focus on the purpose and constraints, not a literal translation of the code.
* **Package Docs:** Every package must have a header: `// Package <name> provides...`
* **Formatting:** Use `[Type]` or `[Package.Type]` for links to other types in comments.

## 5. Coding Style & Rules
* **Go Version:** 1.24+ (utilizing modern toolchains and generics).
* **Error Handling:** Never ignore errors (`_ = ...`). Wrap errors with context: `fmt.Errorf("context: %w", err)`. Use `errors.Is` and `errors.As`.
* **Concurrency:** Prefer channels and `sync` primitives. No `time.Sleep()` in tests—use proper synchronization. Ensure all goroutines have defined lifecycles via `context.Context`.
* **Naming:** `camelCase` for internal, `PascalCase` for exported. Use short, descriptive local names (e.g., `r` for reader, `srv` for server).
* **Project Structure:** Follow idiomatic layout: `/cmd` (binaries)

## 6. Testing Protocol
* **Mandate:** Use `github.com/testcontainers/testcontainers-go` for integration tests.
* **Realism:** When testing the Postgres sniffer, spin up a real Postgres container, send traffic through the Dojo proxy, and assert capture.
* **Standard Library:** Do not use third-party "assertion" libraries (like testify) unless specifically requested. Use standard `if got != want` logic.
* **Environment:** Development is optimized for macOS + Orbstack (Docker).

## 7. Guardrails (DO NOT)
* **No Panic:** Do not use `panic()` for standard error handling.
* **No interface{}:** Do not use `any` unless absolutely necessary; prefer Generics.
* **No init():** Avoid `init()` functions to maintain deterministic startup.
* **No Flakiness:** All tests must pass with the `-race` detector enabled.

## 8. Validation: Always Run the Example Suite
After any change that touches the engine, workspace loader, proxies, adapters, example SUT, or example fixtures, run the full example suite and confirm it passes:

```bash
go run cmd/dojo/main.go ./example/tests/blackbox
```

The example blackbox suite includes **`startup.plan`**, which expects the example SUT to call the mocked Gemini API once during boot (see `example/sut/main.go`). That exercises the startup phase end-to-end.

All tests (including the example suite) must pass before considering a task complete. Unit tests alone are not sufficient — the example suite is the integration smoke test for the entire system.

The CLI is refactored so **`StopProxies` always runs** on non-zero exit (Go `os.Exit` skips `defer`, which previously leaked the SUT on `:8080`). Optional end-to-end check: `go test -tags=integration -race ./cmd/dojo/...` (runs the example suite via a built binary; default `go test ./...` skips it because other packages may use `:8080` in parallel).

**README and suite-authoring guide:** Contributor overview in [readme.md](readme.md). The shipped reference for writing suites (and for using as a Cursor Agent Skill) is **[docs/dojo-skill.md](docs/dojo-skill.md)**. To activate it in Cursor, copy or symlink that file to `.cursor/skills/dojo/SKILL.md` in this repo or in a consumer repo. Do not add a second standalone `SKILL.md` at the repository root.

## 9. Communication
* Do not explain basic programming concepts.
* Upon finishing a step, summarize work, confirm all tests pass, and state the next planned milestone.
* If a dependency issue occurs (e.g., `pgproto3`), resolve via `go mod tidy` before asking the user.
# Dojo

Dojo is a deterministic black-box test orchestrator: it runs a SUT (software under test) behind mocked HTTP and Postgres endpoints, matches outbound traffic against `.plan` files, and optionally evaluates responses with an LLM.

## Documentation

- **[AGENTS.md](AGENTS.md)** — full agent and contributor instructions (TDD, architecture, validation commands).
- **[docs/dojo-skill.md](docs/dojo-skill.md)** — user-facing guide for writing `.plan` suites, fixtures, `startup.plan`, and consumer layouts (also usable as a **Cursor Agent Skill**; see file header for install path).
- **[docs/startup-plan.md](docs/startup-plan.md)** — `startup.plan`: assert on traffic emitted while the SUT starts, before any test `Perform` runs.

## Quick start

```bash
go run ./cmd/dojo/main.go ./example/tests/blackbox
```

The example suite includes a **`startup.plan`** that expects one Gemini call from the example SUT during boot (see `example/sut/main.go`).

## Repository layout

| Path | Purpose |
|------|---------|
| `cmd/dojo/main.go` | CLI entrypoint |
| `internal/engine/` | Suite run, startup phase, matching |
| `internal/workspace/` | Load `dojo.config`, APIs, plans, `startup.plan` |
| `example/sut/` | Example HTTP SUT used by `example/tests/blackbox` |
| `example/tests/blackbox/` | Integration-style example tests + `startup.plan` |
| `docs/dojo-skill.md` | Long-form suite-authoring reference (shipped; Cursor skill install noted in-file) |

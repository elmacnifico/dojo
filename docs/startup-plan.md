# Startup plan (`startup.plan`)

A suite may include a file named **`startup.plan`** in the suite directory (next to `dojo.config`). It is optional.

## Purpose

Some systems call external APIs during process startup (cache warm-up, feature flags, license checks). Normal test plans begin with a **`Perform`** that drives the SUT, so they cannot observe traffic that happens before the first trigger.

The startup plan runs **after** the HTTP (and Postgres) proxies are up and **`API_*_URL` environment variables** are set, but **before** any test from `RunSuite` executes:

1. Proxies start; suite seeds run if configured.
2. If `startup.plan` exists, Dojo registers a synthetic active test (`startup`) and wires its expectations the same way as a normal test plan.
3. The SUT process starts (`sut_command`).
4. Dojo waits until the SUT’s HTTP listener is ready (same TCP readiness as today).
5. Dojo waits until every **`Expect`** in `startup.plan` is satisfied (or times out / SUT crash).
6. The `startup` registration is removed; **`RunSuite` begins** — no folder test runs until step 5 succeeds.

So: **all normal tests run only after the startup plan completes.**

## Syntax

- **Only** `Expect` lines are allowed (no `Perform`, no `Perform -> postgres`, etc.).
- Same `Expect` grammar as test plans, for example:

  ```text
  Expect -> gemini -> Request: startup_gemini_request.json -> Respond: startup_gemini_response.json
  ```

- Fixture paths resolve relative to the **suite directory** (e.g. `example/tests/blackbox/`), same as suite-level assets.

## Matching

Request matching uses the same rules as regular tests (JSON subset match for HTTP, normalized SQL for Postgres). Duplicate-request validation across **named tests** does not include the startup phase; still use a **unique** expected payload per API for the startup probe so it does not collide with a real test’s first expectation if you ever merged logic.

## Example

See **`example/tests/blackbox/`**:

- `startup.plan` — one Gemini expectation.
- `startup_gemini_request.json` / `startup_gemini_response.json` — fixtures.
- **`example/sut/main.go`** — after `ListenAndServe` is running, a background goroutine waits for `/health` then POSTs the probe body to `API_GEMINI_URL`.

## Timeouts

Per-API `timeout` in `apis/*.json` applies to startup expectations the same way as for tests. Increase `expect` or the Gemini API timeout if startup work is slow.

## Logging

- **Console (`--format console`):** when startup expectations all match, Dojo prints the same line shape as for folder tests: **`  PASS  startup.plan  (<duration>)`** (stdout), immediately under the `--- RUNNING SUITE ---` banner and before the first test result.
- **Failures:** log at **ERROR** with `startup plan failed` (stderr); the process exits before any test runs.
- **DEBUG** (`-v` / `--verbose`): preparing expectations, each parsed `Expect` line, registered/waiting messages, and other engine noise (including SUT stdout when verbose).

---
name: dojo
description: >-
  Write and debug Dojo black-box suites: .plan DSL, startup.plan, apis/, dojo.config,
  entrypoints, fixtures, deep-merge, matching. Use for Dojo engine work, example/tests/blackbox,
  example/sut, or consumer repos (e.g. Proofcoach dojo/blackbox).
---
# Writing Dojo Test Suites

**Shipped with the repo (browse, share, vendor):** this file is **`docs/dojo-skill.md`**. It doubles as a Cursor Agent Skill (YAML frontmatter + Markdown body).

**Optional — activate in Cursor:** copy or symlink this file to **`.cursor/skills/dojo/SKILL.md`** in the repo where you author suites (Dojo itself, or a consumer app). Cursor loads skills from that path; the copy in `docs/` stays the canonical version for GitHub and non-Cursor readers.

Dojo is a black-box testing engine. It wraps your application (the SUT) in a
transparent proxy: an **Initiator** sends requests to the SUT, and an
**Observer** intercepts the SUT's outbound HTTP calls and Postgres queries,
matching them against expectations you declare in `.plan` files. You never
modify application code.

## Suite Directory Structure

```text
my_suite/
  dojo.config                       # SUT command, concurrency, timeouts
  startup.plan                      # Optional: only Expect lines; runs after SUT is up, before any test
  apis/
    <api-name>.json                 # One file per external API (mock or live)
  entrypoints/
    <name>.json                     # How Dojo triggers the SUT
  seed/
    schema.sql                      # Shared SQL seed (run once before all tests)
  <fixture>.json                    # Suite-level fixture (deep-merge base)

  test_<name>/
    <name>.plan                     # Exactly one .plan file per test
    incoming.json                   # Perform payload (webhook body, etc.)
    <fixture>.json                  # Test-level fixture (merged onto suite base)
    <fixture>.sql                   # SQL fixture
    apis/
      <api-name>.json              # Test-level API config override (optional)
    entrypoints/
      <name>.json                  # Test-level entrypoint override (optional)
    seed/
      seed.sql                     # Test-specific seed data (optional)
```

Key rules:
- Test directories must start with `test_`.
- Each test directory has exactly one `.plan` file (any name, `.plan` extension).
- Fixture files at both suite and test level with the same name are deep-merged.
- `seed/` can exist at suite level (shared) and test level (per-test data).

### `startup.plan` (optional)

Suite root file **`startup.plan`** may contain **only** `Expect` lines (same syntax as in test plans). Dojo runs it after proxies and env are ready and the SUT accepts TCP, **before** any `RunSuite` test. If it fails, no tests run. Full behavior and logging: repo **`docs/startup-plan.md`**. Example: **`example/tests/blackbox/startup.plan`** and SUT probe in **`example/sut/main.go`**.

## dojo.config

Minimal JSON file at the suite root:

```json
{
  "concurrency": 4,
  "sut_command": "go build -o /tmp/myapp-sut ./cmd/myapp/main.go && exec /tmp/myapp-sut"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `sut_command` | yes | Shell command to start the SUT. Dojo sets env vars so the SUT routes traffic through Dojo's proxies.<br><br>**Golang Best Practice:** Avoid `go run` as it spawns child processes that can become orphaned when Dojo sends a termination signal, leading to port conflicts. Instead, compile and `exec` the binary: `"go build -o /tmp/sut ./main.go && exec /tmp/sut"`. |
| `concurrency` | no | Max parallel test workers (default 1, clamped to >= 1). |
| `evaluator` | no | AI evaluation config (see below). |
| `timeouts` | no | Override default timeouts (see below). |

### Environment files (convention)

Dojo automatically loads `.env` and `.env.local` from the suite directory into
both the Dojo process and the SUT process. This maps Dojo's injected
`API_<NAME>_URL` vars to the SUT's expected env var names, provides static test
configuration, and supplies API keys needed by the evaluator.

- **`.env`** -- committed. URL mappings, test constants.
- **`.env.local`** -- gitignored. Real API keys and secrets.

`.env.local` values override `.env`. Values support `$VAR` expansion against
Dojo's own injected env vars (e.g. `DATABASE_URL=$API_POSTGRES_URL`).

Example `.env`:
```
GEMINI_BASE_URL=$API_GEMINI_URL
WHATSAPP_BASE_URL=$API_WHATSAPP_URL
DATABASE_URL=$API_POSTGRES_URL
ENVIRONMENT=production
```

### Evaluator config (optional)

```json
{
  "evaluator": {
    "provider": "gemini",
    "model": "gemini-2.5-flash",
    "api_key_env": "GEMINI_API_KEY"
  }
}
```

Supported providers: `gemini`, `openai`, `anthropic`.

### Timeouts (optional, Go duration strings)

```json
{
  "timeouts": {
    "perform": "5s",
    "expect": "2s",
    "sut_startup": "90s",
    "sut_shutdown": "5s",
    "ai_evaluator": "30s"
  }
}
```

| Key | Default | Controls |
|-----|---------|----------|
| `perform` | `5s` | Perform trigger HTTP call and live upstream proxy timeout. |
| `expect` | `2s` | How long each Expect waits before timing out. Per-API `timeout` in `apis/*.json` overrides this. |
| `sut_startup` | `90s` | Max wait for SUT to accept TCP connections. |
| `sut_shutdown` | `5s` | Grace period when killing the SUT process. |
| `ai_evaluator` | `30s` | Timeout for AI evaluation LLM calls. |

For real LLM suites (not mocked), set per-API timeout:

```json
{
  "mode": "live",
  "timeout": "30s"
}
```

Or per-test override in `test_slow/apis/gemini.json`:

```json
{
  "timeout": "60s"
}
```

## API Config: `apis/*.json`

Each file defines one external API that the SUT calls. The filename (minus
`.json`) becomes the API name used in `.plan` files.

### Mock HTTP API

```json
{
  "mode": "mock",
  "url": "/v1/messages",
  "default_response": {
    "code": 200,
    "body": "{\"status\":\"ok\"}"
  }
}
```

Dojo intercepts requests to `url` and returns `default_response` (or a
test-specific `Respond:` fixture). The SUT never reaches the real service.

### Live Postgres API

```json
{
  "mode": "live",
  "protocol": "postgres",
  "url": "postgres://user:pass@host:5432/db?sslmode=disable"
}
```

Dojo proxies traffic to the real Postgres instance while sniffing queries for
matching.

### Fields

| Field | Values | Notes |
|-------|--------|-------|
| `mode` | `mock` / `live` | Mock intercepts and replies; live proxies through. |
| `url` | URL path or full URL | Path for mock HTTP (e.g. `/v1/messages`); full postgres:// URL for live Postgres. |
| `protocol` | `postgres` | Only needed for Postgres APIs. HTTP is the default. |
| `default_response` | `{code, body}` | Mock reply when no `Respond:` clause is given. Supports `$VAR` expansion (see below). |

### Env Var Expansion in Mock Responses

`default_response.body` and per-expectation `Respond:` bodies support `$VAR`
expansion using the process environment. This is useful for referencing other
API proxy addresses inside a mock response.

```json
{
  "mode": "mock",
  "default_response": {
    "code": 200,
    "body": "{\"url\": \"$API_MEDIA_DOWNLOAD_URL/file.jpg\"}"
  }
}
```

At runtime `$API_MEDIA_DOWNLOAD_URL` resolves to the actual proxy address,
letting you chain mock APIs (one returns a URL, the SUT follows it back through
another mock).

### Binary File Responses

Mock APIs can serve binary files (images, PDFs, etc.) using `file` and
`content_type` in `default_response`. The `file` path is resolved relative to
the directory containing the API config file (test dir first, suite dir
fallback).

```json
{
  "mode": "mock",
  "default_response": {
    "code": 200,
    "file": "photo.jpg",
    "content_type": "image/jpeg"
  }
}
```

| Field | Description |
|-------|-------------|
| `file` | Path to a binary file. Resolved relative to the API config's directory. |
| `content_type` | `Content-Type` header for the response (defaults to `application/json`). |

Binary file payloads skip `$VAR` expansion to avoid corrupting binary data.

### Test-level API override

Place `test_foo/apis/<api-name>.json` with only the fields that differ. Suite
config is copied first; then the test JSON is merged on top. Test-level
overrides apply even when the plan has no `Expect` clause for that API -- Dojo
uses the override for mock responses whenever that test is the only active test.

This is the primary mechanism for serving test-specific binary fixtures:

```text
test_image_analysis/
  apis/
    media_download.json          # Overrides the suite-level mock with a binary file
  test_image.jpg                 # The binary file served by the override
  image_analysis.plan
```

`test_image_analysis/apis/media_download.json`:
```json
{
  "default_response": {
    "code": 200,
    "file": "test_image.jpg",
    "content_type": "image/jpeg"
  }
}
```

The `file` path is resolved relative to the **test directory first**, then the
suite directory. This keeps test-specific binary fixtures next to their test.

## Entrypoints: `entrypoints/*.json`

Define how Dojo triggers the SUT.

```json
{
  "type": "http",
  "path": "/trigger"
}
```

| Field | Description |
|-------|-------------|
| `type` | Must be `http`. |
| `method` | HTTP method. Defaults to `POST`. Set to `GET`, `PUT`, `DELETE`, etc. as needed. |
| `path` | The SUT endpoint Dojo will call. May include query parameters (e.g. `/webhook?hub.mode=subscribe`). |
| `follow_redirects` | Boolean. Defaults to `true`. Set to `false` to capture redirect responses (3xx) instead of following them. |

### Test-level entrypoint override

Place `test_foo/entrypoints/<name>.json` with only the fields that differ. The
suite-level entrypoint is copied first, then the test JSON is merged on top.
This works identically to test-level API overrides.

Use this when multiple tests hit the same endpoint but differ in one header
(HMAC signatures, API keys, auth tokens):

Suite-level `entrypoints/webhook.json`:
```json
{
  "type": "http",
  "path": "/webhook",
  "headers": {
    "Content-Type": "application/json",
    "X-Signature-Timestamp": "1234567890"
  }
}
```

Test-level `test_valid_sig/entrypoints/webhook.json` (only the diff):
```json
{
  "headers": {
    "X-Signature": "precomputed_base64_hmac"
  }
}
```

Both tests reference the same entrypoint name in the `.plan`:
```text
Perform -> entrypoints/webhook -> Payload: incoming.json -> ExpectStatus: "200"
```

## The `.plan` DSL

Syntax: `Action -> Target -> Clause: value -> Clause: value`

### Actions

| Action | Purpose |
|--------|---------|
| `Perform` | Trigger the SUT or execute a DB assertion. Target is an entrypoint path (e.g. `entrypoints/webhook`) or `postgres` for direct DB queries. |
| `Expect` | Declare an expected outbound call. Target is an API name (e.g. `postgres`, `gemini`). |

### Clauses

| Clause | Used with | Description |
|--------|-----------|-------------|
| `Payload:` | Perform (entrypoint) | Fixture file sent as the trigger body. |
| `ExpectStatus:` | Perform (entrypoint) | Assert the SUT's HTTP response status code (e.g. `"200"`, `"403"`). Without this, Dojo fails on any status >= 400. |
| `Query:` | Perform (postgres) | SQL fixture file to execute against the live database. |
| `Expect:` | Perform (postgres) | Assertion on query result: `"N"` for row count, `file.json` for JSON comparison, or omit for OK-only. |
| `Request:` | Expect | Fixture file containing the expected outbound payload. |
| `Respond:` | Expect | Fixture file returned as the mock response body. |
| `Evaluate Response` | Expect | No value. Triggers AI evaluation using `eval.md`. |

### Example: standard test

```text
Perform -> entrypoints/webhook -> Payload: incoming.json

Expect -> postgres -> Request: postgres_request.sql
Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
Expect -> whatsapp -> Request: whatsapp_request.json
```

Line by line:
1. POST `incoming.json` to the SUT's `/trigger` endpoint.
2. SUT must issue a SQL query matching `postgres_request.sql`.
3. SUT must call Gemini with a body matching `gemini_request.json`; Dojo returns `gemini_response.json`.
4. SUT must call WhatsApp with a body matching `whatsapp_request.json`; Dojo returns `apis/whatsapp.json`'s `default_response`.

### Example: ExpectStatus (health check)

Assert the SUT's HTTP response status code. Useful for health checks, webhook
verification, and testing error responses.

```text
Perform -> entrypoints/health -> ExpectStatus: "200"
```

With `entrypoints/health.json`:
```json
{
  "type": "http",
  "method": "GET",
  "path": "/"
}
```

Without `ExpectStatus`, Dojo fails the test on any status >= 400. With
`ExpectStatus`, Dojo checks for an exact match -- so you can also assert that
a bad request correctly returns an error:

```text
Perform -> entrypoints/webhook_bad_token -> ExpectStatus: "403"
```

### Example: testing OAuth redirects

To test that an auth endpoint returns a redirect without following it, set
`follow_redirects: false` in the entrypoint config:

```json
{
  "type": "http",
  "method": "GET",
  "path": "/auth?userId=1",
  "follow_redirects": false
}
```

```text
Perform -> entrypoints/auth -> ExpectStatus: "307"
```

### Example: testing webhook HMAC signature validation

Test correct and incorrect signatures using the same base entrypoint with
test-level overrides for the signature header:

Suite-level `entrypoints/webhook.json`:
```json
{
  "type": "http",
  "path": "/webhook",
  "headers": {
    "Content-Type": "application/json",
    "X-Signature-Timestamp": "1234567890"
  }
}
```

Test-level `test_valid_sig/entrypoints/webhook.json`:
```json
{
  "headers": {
    "X-Signature": "precomputed_base64_hmac"
  }
}
```

```text
Perform -> entrypoints/webhook -> Payload: incoming.json -> ExpectStatus: "200"
```

Test-level `test_invalid_sig/entrypoints/webhook.json`:
```json
{
  "headers": {
    "X-Signature": "INVALID"
  }
}
```

```text
Perform -> entrypoints/webhook -> Payload: incoming.json -> ExpectStatus: "401"
```

### Example: binary payload

```text
Perform -> entrypoints/upload -> Payload: image.jpg

Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
```

Non-JSON files are sent as raw bytes.

### Example: binary mock response (test-level API override)

When the SUT fetches a binary resource from an external API, override the mock
at the test level to serve a real file. The plan does not need an `Expect`
clause for the mocked API -- the test-level override applies automatically:

```text
Perform -> entrypoints/media_process -> Payload: incoming.json

Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
```

With `test_media_process/apis/media.json`:
```json
{
  "default_response": {
    "code": 200,
    "file": "photo.jpg",
    "content_type": "image/jpeg"
  }
}
```

And `test_media_process/photo.jpg` alongside it. When the SUT calls the media
API, Dojo serves `photo.jpg` with the correct content type.

### Example: ordered multi-expectations

When a SUT makes multiple calls to the same API in one test (e.g., an intent
agent call followed by a conversation agent call to the same Gemini endpoint),
use multiple `Expect` lines. They are matched in declaration order:

```text
Perform -> entrypoints/webhook -> Payload: incoming.json

Expect -> gemini -> Request: intent_request.json -> Respond: intent_response.json
Expect -> gemini -> Request: conv_request.json -> Respond: conv_response.json
Expect -> whatsapp -> Request: whatsapp_request.json
```

The first Gemini call matches `intent_request.json` and gets `intent_response.json`.
The second Gemini call matches `conv_request.json` and gets `conv_response.json`.
Each expectation is fulfilled independently.

### Example: AI evaluation

```text
Perform -> entrypoints/webhook -> Payload: incoming.json

Expect -> gemini -> Request: gemini_request.json -> Evaluate Response
```

Requires `evaluator` in `dojo.config` and an `eval.md` file in the test (or
suite) directory containing grading rules in Markdown.

### Postgres wire protocol verification

`Expect -> postgres` on a live Postgres proxy doesn't just verify the query was
sent -- Dojo parses the pgproto3 wire protocol response to confirm the query
succeeded. An `ErrorResponse` from Postgres fails the expectation automatically.

### Phased execution and `Perform -> postgres`

A plan can contain multiple `Perform` lines. Each `Perform` starts a new
**phase**. All `Expect` lines between two Performs belong to the preceding
phase. The next `Perform` fires only after the previous phase's expectations are
fulfilled.

Use `Perform -> postgres` to query the live database directly after the SUT
finishes and assert on the result:

**Mode 1 -- OK (no Expect):** Query must execute without errors.
```text
Perform -> postgres -> Query: check.sql
```

**Mode 2 -- Row count:** Query must return exactly N rows.
```text
Perform -> postgres -> Query: check.sql -> Expect: "1"
```

**Mode 3 -- JSON comparison:** Result rows compared via subset matching.
```text
Perform -> postgres -> Query: check.sql -> Expect: expected.json
```

### Example: DB state assertion after insert

```text
Perform -> entrypoints/webhook -> Payload: incoming.json

Expect -> gemini -> Request: intent_request.json -> Respond: intent_response.json
Expect -> gemini -> Request: conv_request.json -> Respond: conv_response.json
Expect -> postgres -> Request: postgres_request.sql

Perform -> postgres -> Query: check_insert.sql -> Expect: "1"
```

The second `Perform` runs only after all three `Expect` lines are fulfilled.
`check_insert.sql` queries the database and the test asserts exactly 1 row
exists.

### Runnable example (example suite)

The repo ships `example/tests/blackbox/test_perform_postgres/` which chains all
four `Perform -> postgres` modes in **one plan**:

```text
Perform -> entrypoints/webhook -> Payload: incoming.json

Expect -> postgres -> Request: postgres_request.sql
Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
Expect -> whatsapp -> Request: whatsapp_request.json

Perform -> postgres -> Query: check_row.sql -> Expect: "1"
Perform -> postgres -> Query: check_display.sql -> Expect: expected.json
Perform -> postgres -> Query: ping.sql
Perform -> postgres -> Query: check_gone.sql -> Expect: "0"
```

Run: `go run cmd/dojo/main.go ./example/tests/blackbox`

## Fixture Files

### Format by extension
- `.json` -- JSON body (HTTP APIs). Matched via canonical JSON equality.
- `.sql` -- Raw SQL text (Postgres). Matched via whitespace-collapsed equality.
- Any other extension (`.jpg`, `.png`, `.bin`) -- raw bytes for `Payload:`.

### Fixture placement: what goes where

| Scope | Use for | Location |
|-------|---------|----------|
| Suite level | Shared config (deep-merge bases, shared seeds, default API configs) | `my_suite/<file>`, `my_suite/apis/`, `my_suite/seed/` |
| Test level | Per-test diffs, test-specific payloads, binary fixtures | `test_foo/<file>`, `test_foo/apis/`, `test_foo/seed/` |

**Rule of thumb:** if a fixture is used by only one test, it belongs in the test
directory. Binary files (images, audio, etc.) always belong at the test level
since they are inherently test-specific. Suite-level fixtures should only
contain shared structure that multiple tests deep-merge against.

### Deep merge (fixture inheritance)

When the same filename exists at suite and test level:

1. If both parse as JSON objects, Dojo deep-merges them (suite = base, test = overlay).
2. Nested objects merge recursively. Arrays and scalars in the test file replace the suite value.
3. If either file is not a JSON object, the test file wins outright.

This lets you put shared config at the suite level and only write per-test diffs:

**Suite-level `gemini_request.json`** (shared across all tests):
```json
{
  "generationConfig": { "temperature": 0.7, "maxOutputTokens": 1024 },
  "safetySettings": [...]
}
```

**Test-level `gemini_request.json`** (only the diff):
```json
{
  "contents": [{ "role": "user", "parts": [{ "text": "Delete my account" }] }],
  "systemInstruction": { "parts": [{ "text": "You are a routing assistant." }] }
}
```

At runtime, all four top-level keys are present in the resolved fixture.

## Request Matching

Dojo matches intercepted SUT traffic to active tests using **subset matching**:
the expected fixture only needs to contain the fields you care about. Extra
fields in the actual payload are silently ignored at every nesting level.

### Matching rules
- **HTTP/JSON:** The expected fixture is treated as a JSON subset of the actual
  payload. Every key in the expected object must exist in actual with a matching
  value (compared recursively). Arrays are compared element-by-element at the
  same index; the expected array can be shorter than actual. Scalar values must
  be equal. If either payload is not valid JSON, Dojo falls back to
  whitespace-normalized substring matching.
- **SQL:** Collapse all whitespace to single spaces, strip trailing `;`, then
  check that the normalized expected string is contained in the normalized
  actual query.

This means fixture files only need to specify the fields the test cares about.
A fixture that specifies every field still works identically (it is trivially
a subset of itself).

### Uniqueness constraint

No two tests in a suite may share an identical normalized expected request for
the same API. Dojo rejects exact duplicates at load time. If two different
subset fixtures both match the same actual request at runtime, Dojo reports an
ambiguous match error.

## Running a Suite

```bash
go run cmd/dojo/main.go ./path/to/my_suite

# With output artifacts:
go run cmd/dojo/main.go --format json -o results/ ./path/to/my_suite
```

| Flag | Values | Description |
|------|--------|-------------|
| `--format` | `console` (default), `json`, `jsonl` | Output format. |
| `-o` / `--output` | directory path | Write `summary.json` and `summary.md` to disk. |

Dojo will:
1. Read `dojo.config` and set proxy env vars.
2. Boot the SUT as a child process; wait for its HTTP listener if configured.
3. If **`startup.plan`** exists at the suite root, satisfy those `Expect` lines before any test runs (failure aborts the whole suite).
4. Run all tests concurrently (up to `concurrency`).
5. Print the verdict and exit 0 (all pass) or 1 (any failure).

After changing engine, workspace, proxies, or example SUT/fixtures, also run **`go test ./...`** from the Dojo module root.

## Checklist: Adding a New Test

1. Create `test_<name>/` inside the suite directory.
2. Create the `.plan` file with `Perform` and `Expect` lines.
3. Create `incoming.json` (or whatever your `Payload:` references).
4. For each `Expect -> <api>`:
   - Create the `Request:` fixture (`.json` or `.sql`).
   - If mock: create `Respond:` fixture or rely on `default_response`.
5. If the SUT calls a mock API that needs a test-specific response (especially binary files): create `test_<name>/apis/<api>.json` with a `file` and `content_type` override. Place the binary file in the test directory.
6. If using deep merge: only put the per-test diff in `test_<name>/`, keep the shared base at suite level.
7. If the test needs seed data: create `test_<name>/seed/seed.sql`.
8. If the test needs a different API config: create `test_<name>/apis/<api>.json` with only the overridden fields.
9. If the test needs different entrypoint headers (e.g., HMAC signatures, API keys): create `test_<name>/entrypoints/<name>.json` with only the overridden fields.
10. Run: `go run cmd/dojo/main.go ./path/to/suite` and confirm the test passes.

For deeper details, see [readme.md](readme.md).

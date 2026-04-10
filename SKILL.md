---
name: use-dojo
description: >-
  Write Dojo black-box test suites for a Software Under Test. Covers .plan DSL,
  fixture files, apis/ config, dojo.config, entrypoints, deep-merge, and
  matching rules. Use when creating or editing Dojo test suites, .plan files,
  blackbox tests, or dojo fixtures.
---
# Writing Dojo Test Suites

Dojo is a black-box testing engine. It wraps your application (the SUT) in a
transparent proxy: an **Initiator** sends requests to the SUT, and an
**Observer** intercepts the SUT's outbound HTTP calls and Postgres queries,
matching them against expectations you declare in `.plan` files. You never
modify application code.

## Suite Directory Structure

```text
my_suite/
  dojo.config                       # SUT command, concurrency, timeouts
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
    seed/
      seed.sql                     # Test-specific seed data (optional)
```

Key rules:
- Test directories must start with `test_`.
- Each test directory has exactly one `.plan` file (any name, `.plan` extension).
- Fixture files at both suite and test level with the same name are deep-merged.
- `seed/` can exist at suite level (shared) and test level (per-test data).

## dojo.config

Minimal JSON file at the suite root:

```json
{
  "concurrency": 4,
  "sut_command": "go run ./cmd/myapp/main.go"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `sut_command` | yes | Shell command to start the SUT. Dojo sets env vars so the SUT routes traffic through Dojo's proxies. |
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
    "sut_startup": "90s",
    "sut_shutdown": "5s",
    "http_client": "5s",
    "ai_evaluator": "30s"
  }
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
| `default_response` | `{code, body}` | Mock reply when no `Respond:` clause is given. |

### Test-level API override

Place `test_foo/apis/<api-name>.json` with only the fields that differ. Suite
config is copied first; then the test JSON is merged on top.

```json
{
  "default_response": {
    "code": 200,
    "body": "{\"updated\":true}"
  }
}
```

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
| `path` | The SUT endpoint Dojo will POST to. |

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

### Example: binary payload

```text
Perform -> entrypoints/upload -> Payload: image.jpg

Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
```

Non-JSON files are sent as raw bytes.

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
2. Boot the SUT as a child process.
3. Run all tests concurrently (up to `concurrency`).
4. Print the verdict and exit 0 (all pass) or 1 (any failure).

## Checklist: Adding a New Test

1. Create `test_<name>/` inside the suite directory.
2. Create the `.plan` file with `Perform` and `Expect` lines.
3. Create `incoming.json` (or whatever your `Payload:` references).
4. For each `Expect -> <api>`:
   - Create the `Request:` fixture (`.json` or `.sql`).
   - If mock: create `Respond:` fixture or rely on `default_response`.
5. If using deep merge: only put the per-test diff in `test_<name>/`, keep the shared base at suite level.
6. If the test needs seed data: create `test_<name>/seed/seed.sql`.
7. If the test needs a different API config: create `test_<name>/apis/<api>.json` with only the overridden fields.
8. Run: `go run cmd/dojo/main.go ./path/to/suite` and confirm the test passes.

For deeper details, see [readme.md](readme.md).

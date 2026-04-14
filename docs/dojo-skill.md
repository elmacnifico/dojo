---
name: dojo
description: >-
  Write and debug Dojo black-box suites: .plan DSL, startup.plan, dojo.yaml,
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
  dojo.yaml                         # SUT command, concurrency, timeouts, APIs, and entrypoints
  startup.plan                      # Optional: only Expect lines; runs after SUT is up, before any test
  seed/
    schema.sql                      # Shared SQL seed (run once before all tests)
  <fixture>.json                    # Suite-level fixture (deep-merge base)

  test_<name>/
    <name>.plan                     # Exactly one .plan file per test
    dojo.yaml                       # Test-level overrides for APIs and entrypoints (optional)
    <fixture>.json                  # Test-level fixture (merged onto suite base)
    <fixture>.sql                   # SQL fixture
    seed/
      seed.sql                     # Test-specific seed data (optional)
```

Key rules:
- Test directories must start with `test_`.
- Each test directory has exactly one `.plan` file (any name, `.plan` extension).
- Fixture files at both suite and test level with the same name are deep-merged.
- `seed/` can exist at suite level (shared) and test level (per-test data).

### `startup.plan` (optional)

Suite root file **`startup.plan`** may contain **only** `Expect` lines (same syntax as in test plans). Dojo runs it after proxies and env are ready and the SUT accepts TCP, **before** any `RunSuite` test. If it fails, no tests run. Full behavior and logging is documented in the repo **`readme.md`**. Example: **`example/tests/blackbox/startup.plan`** and SUT probe in **`example/sut/main.go`**.

## dojo.yaml

Central configuration file at the suite root:

```yaml
concurrency: 4
sut_command: "go build -o /tmp/myapp-sut ./cmd/myapp/main.go && exec /tmp/myapp-sut"
sut_base_url: "http://127.0.0.1:8080" # Default base URL for inline HTTP triggers

timeouts:
  perform: 5s
  expect: 2s

apis:
  gemini:
    mode: mock
    url: "/v1beta/models/gemini-2.5-flash:generateContent"
  postgres:
    mode: live
    protocol: postgres
    url: "postgres://user:pass@host:5432/db?sslmode=disable"
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

```yaml
evaluator:
  provider: gemini
  model: gemini-2.5-flash
  api_key_env: GEMINI_API_KEY
```

Supported providers: `gemini`, `openai`, `anthropic`.

### Timeouts (optional, Go duration strings)

```yaml
timeouts:
  perform: 5s
  expect: 2s
  sut_startup: 90s
  sut_shutdown: 5s
  ai_evaluator: 30s
```

| Key | Default | Controls |
|-----|---------|----------|
| `perform` | `5s` | Perform trigger HTTP call and live upstream proxy timeout. |
| `expect` | `2s` | How long each Expect waits before timing out. Per-API `timeout` in `dojo.yaml` overrides this. |
| `sut_startup` | `90s` | Max wait for SUT to accept TCP connections. |
| `sut_shutdown` | `5s` | Grace period when killing the SUT process. |
| `ai_evaluator` | `30s` | Timeout for AI evaluation LLM calls. |

For real LLM suites (not mocked), set per-API timeout:

```yaml
apis:
  gemini:
    mode: live
    timeout: 30s
```

Or per-test override in `test_slow/dojo.yaml`:

```yaml
apis:
  gemini:
    timeout: "60s"
```

## API Config in `dojo.yaml`

APIs are defined under the `apis` key in `dojo.yaml`. The key becomes the API name used in `.plan` files.

### Mock HTTP API

```yaml
apis:
  whatsapp:
    mode: mock
    url: "/v1/messages"
    default_response:
      code: 200
      body: '{"status":"ok"}'
```

Dojo intercepts requests to `url` and returns `default_response` (or a
test-specific `Respond:` fixture). The SUT never reaches the real service.

### Live Postgres API

```yaml
apis:
  postgres:
    mode: live
    protocol: postgres
    url: "postgres://user:pass@host:5432/db?sslmode=disable"
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

```yaml
apis:
  media_lookup:
    mode: mock
    default_response:
      code: 200
      body: '{"url": "$API_MEDIA_DOWNLOAD_URL/file.jpg"}'
```

At runtime `$API_MEDIA_DOWNLOAD_URL` resolves to the actual proxy address,
letting you chain mock APIs (one returns a URL, the SUT follows it back through
another mock).

### Binary File Responses

Mock APIs can serve binary files (images, PDFs, etc.) using `file` and
`content_type` in `default_response`. The `file` path is resolved relative to
the directory containing the API config file (test dir first, suite dir
fallback).

```yaml
apis:
  media:
    mode: mock
    default_response:
      code: 200
      file: photo.jpg
      content_type: image/jpeg
```

| Field | Description |
|-------|-------------|
| `file` | Path to a binary file. Resolved relative to the API config's directory. |
| `content_type` | `Content-Type` header for the response (defaults to `application/json`). |

Binary file payloads skip `$VAR` expansion to avoid corrupting binary data.

### Test-level API override

Place `test_foo/dojo.yaml` with only the fields that differ under `apis:`. Suite
config is copied first; then the test YAML is merged on top. Test-level
overrides apply even when the plan has no `Expect` clause for that API -- Dojo
uses the override for mock responses whenever that test is the only active test.

This is the primary mechanism for serving test-specific binary fixtures:

```text
test_image_analysis/
  dojo.yaml                      # Overrides the suite-level mock with a binary file
  test_image.jpg                 # The binary file served by the override
  image_analysis.plan
```

`test_image_analysis/dojo.yaml`:
```yaml
apis:
  media_download:
    default_response:
      code: 200
      file: "test_image.jpg"
      content_type: "image/jpeg"
```

The `file` path is resolved relative to the **test directory first**, then the
suite directory. This keeps test-specific binary fixtures next to their test.

## Entrypoints in `dojo.yaml`

Define named entrypoints under the `entrypoints` key in `dojo.yaml` for complex triggers (like following redirects). For simple HTTP calls, prefer **inline HTTP triggers** directly in the `.plan` file (e.g., `Perform -> POST /trigger`).

```yaml
entrypoints:
  auth_redirect:
    type: http
    method: GET
    path: "/auth?state=abc123"
    follow_redirects: false
```

| Field | Description |
|-------|-------------|
| `type` | Must be `http`. |
| `method` | HTTP method. Defaults to `POST`. Set to `GET`, `PUT`, `DELETE`, etc. as needed. |
| `path` | The SUT endpoint Dojo will call. May include query parameters (e.g. `/webhook?hub.mode=subscribe`). |
| `follow_redirects` | Boolean. Defaults to `true`. Set to `false` to capture redirect responses (3xx) instead of following them. |

### Test-level entrypoint override

Place `test_foo/dojo.yaml` with only the fields that differ under `entrypoints:`. The
suite-level entrypoint is copied first, then the test YAML is merged on top.
This works identically to test-level API overrides.

Use this when multiple tests hit the same endpoint but differ in one header
(HMAC signatures, API keys, auth tokens):

Suite-level `dojo.yaml`:
```yaml
entrypoints:
  webhook:
    type: http
    path: "/webhook"
    headers:
      Content-Type: "application/json"
      X-Signature-Timestamp: "1234567890"
```

Test-level `test_valid_sig/dojo.yaml` (only the diff):
```yaml
entrypoints:
  webhook:
    headers:
      X-Signature: "precomputed_base64_hmac"
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
| `Perform` | Trigger the SUT, pause, or run a DB assertion. Target is an entrypoint path (e.g. `entrypoints/webhook`), `postgres` for direct DB queries, or `wait` for a timed pause between phases. |
| `Expect` | Declare an expected outbound call. Target is an API name (e.g. `postgres`, `gemini`). |

### Clauses

| Clause | Used with | Description |
|--------|-----------|-------------|
| `Payload:` | Perform (entrypoint) | Fixture file sent as the trigger body. |
| `ExpectStatus:` | Perform (entrypoint) | Assert the SUT's HTTP response status code (e.g. `"200"`, `"403"`). Without this, Dojo fails on any status >= 400. |
| `Query:` | Perform (postgres) | SQL fixture file to execute against the live database. |
| `Expect:` | Perform (postgres) | Assertion on query result: `"N"` for row count, `file.json` for JSON comparison, or omit for OK-only. |
| `Duration:` | Perform (wait) | Go duration string (e.g. `500ms`, `2s`). Alternatively use a single positional duration: `Perform -> wait -> 250ms`. No `Expect` lines may follow a wait `Perform` in the same phase. |
| `Request:` | Expect | Fixture file containing the expected outbound payload. |
| `Respond:` | Expect | Fixture file returned as the mock response body. Cannot be used with live APIs. |
| `MaxCalls:` | Expect | Number of times this expectation must be matched before it is fulfilled. Defaults to 1. |
| `Evaluate Response` | Expect | No value. Triggers AI evaluation using `eval.md`. |

### Example: standard test

```text
Perform -> POST /webhook -> Payload: incoming.json

Expect -> postgres
Expect -> gemini
Expect -> whatsapp
```

Line by line:
1. POST `incoming.json` to the SUT's `/trigger` endpoint.
2. SUT must issue a SQL query matching `postgres_request.sql`.
3. SUT must call Gemini with a body matching `gemini_request.json`; Dojo returns `gemini_response.json`.
4. SUT must call WhatsApp with a body matching `whatsapp_request.json`; Dojo returns the API's `default_response` from `dojo.yaml`.

**Convention-based fixture resolution:** When an `Expect` line omits `Request:`
and `Respond:` clauses, Dojo resolves fixtures by naming convention:
`<api>_request.json` (or `<api>_request.sql` for Postgres) and
`<api>_response.json`. The files are looked up in the test directory first,
then the suite directory, with deep-merge if both exist.

### Example: ExpectStatus (health check)

Assert the SUT's HTTP response status code. Useful for health checks, webhook
verification, and testing error responses.

```text
Perform -> entrypoints/health -> ExpectStatus: "200"
```

With `dojo.yaml` containing:
```yaml
entrypoints:
  health:
    type: http
    method: GET
    path: "/"
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

```yaml
entrypoints:
  auth:
    type: http
    method: GET
    path: "/auth?userId=1"
    follow_redirects: false
```

```text
Perform -> entrypoints/auth -> ExpectStatus: "307"
```

### Example: testing webhook HMAC signature validation

Test correct and incorrect signatures using the same base entrypoint with
test-level overrides for the signature header:

Suite-level `dojo.yaml`:
```yaml
entrypoints:
  webhook:
    type: http
    path: "/webhook"
    headers:
      Content-Type: "application/json"
      X-Signature-Timestamp: "1234567890"
```

Test-level `test_valid_sig/dojo.yaml`:
```yaml
entrypoints:
  webhook:
    headers:
      X-Signature: "precomputed_base64_hmac"
```

```text
Perform -> entrypoints/webhook -> Payload: incoming.json -> ExpectStatus: "200"
```

Test-level `test_invalid_sig/dojo.yaml`:
```yaml
entrypoints:
  webhook:
    headers:
      X-Signature: "INVALID"
```

```text
Perform -> entrypoints/webhook -> Payload: incoming.json -> ExpectStatus: "401"
```

### Example: binary payload

```text
Perform -> POST /upload -> Payload: image.jpg

Expect -> gemini
```

Non-JSON files are sent as raw bytes.

### Example: binary mock response (test-level API override)

When the SUT fetches a binary resource from an external API, override the mock
at the test level to serve a real file. The plan does not need an `Expect`
clause for the mocked API -- the test-level override applies automatically:

```text
Perform -> POST /media-process -> Payload: incoming.json

Expect -> gemini
```

With `test_media_process/dojo.yaml` overriding the media API:

```yaml
apis:
  media:
    default_response:
      code: 200
      file: photo.jpg
      content_type: image/jpeg
```

And `test_media_process/photo.jpg` alongside it. When the SUT calls the media
API, Dojo serves `photo.jpg` with the correct content type.

### Example: ordered multi-expectations

When a SUT makes multiple calls to the same API in one test (e.g., an intent
agent call followed by a conversation agent call to the same Gemini endpoint),
use multiple `Expect` lines. They are matched in declaration order:

```text
Perform -> POST /webhook -> Payload: incoming.json

Expect -> gemini -> Request: intent_request.json -> Respond: intent_response.json
Expect -> gemini -> Request: conv_request.json -> Respond: conv_response.json
Expect -> whatsapp
```

The first Gemini call matches `intent_request.json` and gets `intent_response.json`.
The second Gemini call matches `conv_request.json` and gets `conv_response.json`.
Each expectation is fulfilled independently.

### Example: MaxCalls (variable repeat expectations)

When the SUT makes a variable number of calls to the same API -- LLM
tool-calling loops, retry patterns, pagination -- use `MaxCalls:` to allow an
expectation to match up to N times:

```text
Perform -> POST /webhook -> Payload: incoming.json

Expect -> gemini -> Request: tool_call.json -> MaxCalls: "5"
Expect -> gemini -> Request: final_call.json -> Respond: final_response.json
Expect -> whatsapp
```

Semantics: **greedy with lookahead**. The engine consumes up to N matches for
the current expectation, but moves on early if an incoming request matches the
*next* expectation instead. In the example above, if the SUT makes 3 tool calls
then sends the final call, Dojo fulfills the first expectation at 3 (not 5) and
advances to the second.

`MaxCalls:` cannot be combined with `Respond:`. It is designed for `live` APIs
where Dojo proxies to the real upstream -- not for mocked APIs where Dojo
provides canned responses.

### Example: AI evaluation

```text
Perform -> POST /webhook -> Payload: incoming.json

Expect -> gemini -> Request: gemini_request.json -> Evaluate Response
```

Requires `evaluator` in `dojo.yaml` and an `eval.md` file in the test (or
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

**`Perform -> wait`** (optional between phases) sleeps for a positive Go
duration before the next phase. Example: `Perform -> wait -> Duration: 1s` or
`Perform -> wait -> 1ms`. Do not put `Expect` lines after a wait `Perform` in
the same phase.

Use `Perform -> postgres` to query the live database directly after the SUT
finishes and assert on the result:

**Mode 1 -- OK (no Expect):** Query must execute without errors.
```text
Perform -> postgres -> check.sql
```

**Mode 2 -- Row count:** Query must return exactly N rows.
```text
Perform -> postgres -> check.sql -> "1"
```

**Mode 3 -- JSON comparison:** Result rows compared via subset matching.
```text
Perform -> postgres -> check.sql -> expected.json
```

### Example: DB state assertion after insert

```text
Perform -> POST /webhook -> Payload: incoming.json

Expect -> gemini -> Request: intent_request.json -> Respond: intent_response.json
Expect -> gemini -> Request: conv_request.json -> Respond: conv_response.json
Expect -> postgres

Perform -> postgres -> check_insert.sql -> "1"
```

The second `Perform` runs only after all three `Expect` lines are fulfilled.
`check_insert.sql` queries the database and the test asserts exactly 1 row
exists.

### Runnable example (example suite)

The repo ships `example/tests/blackbox/test_perform_wait/` with a short plan
that uses **`Perform -> wait -> Duration: 1ms`** between outbound expectations
and a `Perform -> postgres` check.

`example/tests/blackbox/test_perform_postgres/` chains all four
`Perform -> postgres` modes (and a positional `Perform -> wait -> 1ms`) in **one plan**:

```text
Perform -> POST /webhook -> Payload: incoming.json

Expect -> postgres
Expect -> gemini
Expect -> whatsapp

Perform -> postgres -> check_row.sql -> "1"
Perform -> postgres -> check_display.sql -> expected.json
Perform -> postgres -> ping.sql
Perform -> postgres -> check_gone.sql -> "0"
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
| Suite level | Shared config (deep-merge bases, shared seeds, default API configs) | `my_suite/<file>`, `my_suite/dojo.yaml`, `my_suite/seed/` |
| Test level | Per-test diffs, test-specific payloads, binary fixtures | `test_foo/<file>`, `test_foo/dojo.yaml`, `test_foo/seed/` |

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

### Envelope fixtures (header + body matching)

When the SUT sends outbound HTTP requests where the unique identifier is in a
header (e.g., `Authorization: Bearer <token>`) rather than in the body, use
an **envelope fixture**. The existing `Request:` clause gains an auto-detected
format — no DSL changes needed.

A `Request:` fixture file is treated as an envelope when it is valid JSON with
exactly two top-level keys: `"headers"` (object) and `"body"`.

```json
{
  "headers": { "Authorization": "Bearer mock_token_user_A" },
  "body": "action=getmeas"
}
```

**Body value semantics:**
- **String** (`"body": "action=getmeas"`) — raw bytes, matched with whitespace-collapsed substring match (same as non-JSON fallback).
- **Object/array** (`"body": {"to": "491234500700"}`) — matched with JSON subset matching.
- **Empty string** (`"body": ""`) — wildcard, matches any body.

**Header matching:** Actual HTTP headers are flattened to `{"Header-Name": "first-value", ...}` and matched with JSON subset matching against the `"headers"` value. Both body AND headers must match for a hit.

This is the key tool for concurrent tests against APIs that use bearer tokens
or API keys in headers with identical form-encoded bodies.

### Uniqueness constraint

No two tests in a suite may share an identical normalized expected request for
the same API. Dojo rejects exact duplicates at load time when `concurrency > 1`,
or when `strict_duplicate_expects: true` is set in suite `dojo.yaml` (useful to
catch ambiguous fixtures before raising concurrency). When envelope
fixtures are used, the headers are included in the dedup key — two expectations
with the same body but different headers are considered distinct. If two
different subset fixtures both match the same actual request at runtime, Dojo
reports an ambiguous match error.

### Ordered expectations and concurrency

Multiple `Expect -> sameAPI` lines within a single test are matched in
declaration order. The engine serializes mock correlation (`ProcessRequest` /
`ProcessResponse`) so parallel SUT goroutines cannot double-fulfill the same
expectation index. Prefer **specific** request fixtures for HTTP subset
match -- tiny fixtures (e.g. only `generationConfig`) can match unrelated
outbound calls and make suites flaky when the SUT runs background work.

## Best Practices: Fixtures and Concurrency

### Minimize Useless JSONs

Because Dojo uses subset matching, you should **never copy-paste massive payloads** from your application logs into your fixture files. 

Instead, only include the fields that are directly relevant to the test's assertion.
- **Reduces noise:** It is immediately obvious to a reader what the test is actually verifying.
- **Reduces brittleness:** If an unrelated field in the SUT's outbound payload changes (like a timestamp, an unrelated config flag, or a new optional field), the test will not break.

### Concurrency-Safe Fixtures

When running tests in parallel (`concurrency > 1` in `dojo.yaml`), overly generic fixtures can cause flaky tests. 

If your fixture is too small (e.g., `{ "generationConfig": { "temperature": 0.7 } }`), it might accidentally match background traffic emitted by the SUT, or it might match a request triggered by a *different* parallel test.

To make fixtures concurrency-safe:
- **Include unique identifiers:** Always include a field that uniquely ties the outbound request to the specific test trigger. For example, include the specific `user_id`, `session_id`, or a distinct string in the prompt/payload that is unique to that test case.
- **Avoid overly generic subset matches:** Ensure your subset is specific enough that it cannot plausibly match another test's traffic.
- **Use envelope fixtures for header-based disambiguation:** When the SUT's outbound calls differ only in headers (e.g., OAuth bearer tokens), use envelope fixtures so each test matches on its unique token:

```
# test_withings_user_A/withings_request.json
{"headers": {"Authorization": "Bearer token_user_A"}, "body": "action=getmeas"}

# test_withings_user_B/withings_request.json
{"headers": {"Authorization": "Bearer token_user_B"}, "body": "action=getmeas"}
```

Both tests share the same body but are disambiguated by header values, enabling safe concurrent execution.

By combining these practices—minimizing useless fields, retaining unique identifiers, and using envelope fixtures for header-based APIs—you create suites that are both resilient to refactoring and safe to run at high concurrency.

## Testing Agent Chains

When testing a system composed of multiple AI agents (e.g., Image Agent -> Intent Agent -> Conversation Agent), you should isolate each agent to prevent cascading failures and pinpoint errors.

### Isolation Strategy

To test an intermediate agent in the chain:
1. **Make the Target Agent Live**: The agent you are testing must call the real LLM API (`mode: live`).
2. **Mock Downstream Agents**: Any agent that runs *after* the target agent must be mocked (`mode: mock`). This stops the chain from continuing and saves tokens/time.
3. **Evaluate the Handoff Payload**: Instead of evaluating the final output of the entire system (which won't exist because downstream agents are mocked), evaluate the *request* sent to the first mocked downstream agent.

### Example: Testing an "Intent" Agent

Suppose your pipeline is `Image -> Intent -> Conversation -> WhatsApp`. To test the `Intent` agent:

1. **`dojo.yaml` configuration**:
   - `gemini_image`: `mock` (Provide a static image analysis so the Intent agent has deterministic input)
   - `gemini_intent`: `live` (The agent under test)
   - `gemini_conv`: `mock` (Stop the chain here)
   - `whatsapp`: `mock`

2. **`.plan` file**:
   ```text
   Perform -> entrypoints/webhook -> Payload: incoming.json
   Expect -> gemini_conv -> Evaluate Response
   ```
   *Note: We expect a call to `gemini_conv` because that is the next step in the SUT's code after the Intent agent finishes. By evaluating this request, we can inspect the exact output produced by the Intent agent.*

3. **`eval.md` (Grading Rubric)**:
   Instruct the AI Evaluator on how to extract the target agent's output from the downstream request payload:
   ```markdown
   You are evaluating the Intent Agent. The ACTUAL PAYLOAD is the HTTP request sent to the Conversation Agent.
   Inside the ACTUAL PAYLOAD, look at `contents[0].parts[0].text`. This contains a JSON object called "Shared Context".
   Find the `intent_result` field inside that Shared Context. This is the output of the Intent Agent.

   Evaluate `intent_result` based on these criteria:
   ...
   ```

This strategy ensures that each suite tests exactly one agent's logic, making failures unambiguous and evaluation highly precise.

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
| `--trace` | none | Trace log HTTP and Postgres request/response payloads (truncated) correlated by test id. |

Dojo will:
1. Read `dojo.yaml` and set proxy env vars.
2. Boot the SUT as a child process; wait for its HTTP listener if configured.
3. If **`startup.plan`** exists at the suite root, satisfy those `Expect` lines before any test runs (failure aborts the whole suite).
4. Run all tests concurrently (up to `concurrency`).
5. Print the verdict and exit 0 (all pass) or 1 (any failure).

After changing engine, workspace, proxies, or example SUT/fixtures, also run **`go test ./...`** from the Dojo module root.

### Tracing & LLM Usage

Pass `--trace` to log all HTTP and Postgres traffic (truncated, correlated by
test ID) to stderr via `slog`. This does not affect `--format` output.

Dojo parses token usage from live LLM JSON (Gemini `usageMetadata`, OpenAI
`usage`, Anthropic-style `input_tokens` / `output_tokens`, etc.). **Console**
does not print token usage by default; use **`--llm-usage`** for tabular
per-test and suite breakdowns (the flag is honored even after `run` or the
suite path). **JSON / jsonl** always expose `llm_usage`,
`llm_usage_by_api`, and `llm_usage_derived` when present.

## Checklist: Adding a New Test

1. Create `test_<name>/` inside the suite directory.
2. Create the `.plan` file with `Perform` and `Expect` lines.
3. Create `incoming.json` (or whatever your `Payload:` references).
4. For each `Expect -> <api>`:
   - Create the `Request:` fixture (`.json` or `.sql`).
   - If mock: create `Respond:` fixture or rely on `default_response`.
5. If the SUT calls a mock API that needs a test-specific response (especially binary files): create `test_<name>/dojo.yaml` with a `file` and `content_type` override. Place the binary file in the test directory.
6. If using deep merge: only put the per-test diff in `test_<name>/`, keep the shared base at suite level.
7. If the test needs seed data: create `test_<name>/seed/seed.sql`.
8. If the test needs a different API config: create `test_<name>/dojo.yaml` with only the overridden fields.
9. If the test needs different entrypoint headers (e.g., HMAC signatures, API keys): create `test_<name>/dojo.yaml` with only the overridden fields.
10. Run: `go run cmd/dojo/main.go ./path/to/suite` and confirm the test passes.

For deeper details, see [readme.md](readme.md).

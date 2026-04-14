# Project Dojo

**A Black-Box Contract Engine for Agentic Software Development**

Dojo is a declarative testing engine built in Go. It acts as a transparent
Man-in-the-Middle proxy between your Software Under Test (SUT) and its
dependencies. By intercepting HTTP traffic and raw Postgres queries, Dojo lets
you assert, mock, or AI-evaluate your application's behavior entirely from the
outside -- without touching a single line of application code.

---

## Why Dojo?

Traditional unit tests are implementation-heavy and tightly coupled to code
structure. When an AI coding agent refactors your codebase, internal mocks and
unit tests break, grinding autonomous development to a halt. Humans end up
spending more time fixing the agent's tests than the agent spent writing the
feature.

**Dojo solves the AI coding bottleneck.** Because Dojo tests your application
as a complete Black Box -- validating only the raw database queries and HTTP
requests it puts on the wire -- your tests become **implementation-agnostic
contracts**.

- **You act as the Architect:** You write the `.plan` file and configure the
`dojo.yaml` file to define *what* the app should do and what side effects it
must produce.
- **The Agent acts as the Builder:** Your AI coding agent figures out *how* to
code it. It can use any language, any design pattern, and refactor endlessly.
- **Dojo acts as the Judge:** If the suite passes, the agent's code is correct.
Dojo is the deterministic guardrail for autonomous code generation.

---

### Use Cases

By decoupling the test from the implementation, Dojo enables testing workflows
that are traditionally nightmares to maintain:

- **Prompt Regression and Agent Tool-Use:** Stop mocking your LLM agent's
internal reasoning. Send a prompt via Dojo and listen on the wire to see if
the agent actually triggered the correct webhook or Postgres `UPDATE`.
- **AI-to-AI Evaluations:** Upgrading to a new LLM model? Pump 500 historical
chat logs through Dojo and use the `Evaluate Response` directive to have a
second LLM grade the new model's output against your brand guidelines.
- **The "Strangler Fig" Migration:** Rewriting a 10-year-old Python backend
into Go? Point the exact same Dojo suite at both apps. If the Go app produces
the exact same Postgres `INSERT` statements and HTTP responses, your migration
is bug-for-bug compatible.
- **Chaos Engineering:** Configure a mock in your `dojo.yaml` file to force the
Stripe API to timeout or return a `502`. Verify on the wire that your app
caught the error and rolled back the database transaction instead of crashing.

---

## Getting Started

- Go 1.24+
- Docker (for integration tests and spinning up dependencies)

### Running a Suite

```bash
# From the project root:
go run cmd/dojo/main.go ./example/tests/blackbox

# Or after building:
dojo ./example/tests/blackbox
dojo --format json -o results/ ./example/tests/blackbox

# Usage (works with optional `run`, e.g. `dojo run --help`):
dojo --help
```

The example blackbox suite starts the SUT on **port 29473** (see `sut_base_url` /
`PORT` in `example/tests/blackbox/dojo.yaml`) so another process on **:8080**
does not block the run. The **eval** example suite (`./example/tests/eval`) uses
**port 29474** the same way and needs a valid **`GEMINI_API_KEY`** for
`Evaluate Response` checks (see `example/tests/eval/.env` / `.env.local`).

Dojo will:

1. Load `.env` and `.env.local` from the suite directory (if present) into the
  process environment.
2. Read `dojo.yaml` and configure environment variables to route SUT traffic
  through Dojo's local proxies.
3. Boot your application as a child process (when `sut_command` is set).
  *Note for Go projects: Avoid using `go run` as your `sut_command`. `go run` spawns a child process for the binary, which can become orphaned when Dojo sends a termination signal, leading to port conflicts on subsequent runs. Instead, compile and `exec` the binary:*
   `"sut_command": "go build -o /tmp/sut-bin ./main.go && exec /tmp/sut-bin"`
4. Spin up concurrent test workers (up to the configured `concurrency` limit).
5. Execute all `Perform` triggers and wait for `Expect` matches.
6. Tear down the application and print the verdict (pass `--output` to write
  `summary.json` and `summary.md` to disk).

## Architecture: Initiator and Observer

Dojo operates using two core personas simultaneously to encapsulate the SUT:

1. **The Initiator (Trigger):** Acts as the upstream client. It executes the
  `Perform` step by proactively hitting your SUT's entrypoints (e.g., sending
   a webhook payload).
2. **The Observer (Proxy):** Acts as the downstream dependency. It intercepts
  your SUT's outbound requests, matches them against the `Expect` steps in
   your plan, and either passes them through to the real service or returns a
   mock response.

---

## Writing a Test Suite

### Convention Over Configuration

Dojo separates **what to test** from **how to connect**. Technical wiring --
URLs, protocols, timeouts -- lives in the `apis` section of `dojo.yaml`. The `.plan` file names
every fixture explicitly so there is no ambiguity about which payloads are
expected. Nothing is auto-discovered or implicitly wired.

### The Plan Names Every Fixture

Every `Expect` line in the `.plan` uses a `Request:` clause to name the fixture
file that holds the expected outbound payload. Use a `Respond:` clause to name
the mock response file. Fixture files use the natural extension for their
content: `.json` for HTTP request bodies, `.sql` for Postgres queries.

```text
Expect -> postgres -> Request: postgres_request.sql
Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
Expect -> whatsapp -> Request: whatsapp_request.json
```

Fixture files are resolved from the **test directory first**, falling back to
the **suite directory**. When the same filename exists in both and both are
valid JSON objects, they are deep-merged (see below).

### Directory Hierarchy

Technical API configuration flows from suite to test:

- The `apis` section of the suite-level `dojo.yaml` defines shared config
(URLs, mode, timeouts, default responses).
- A test-level `dojo.yaml` overlays only the fields that differ. The suite
config is copied first, then the test YAML is applied on top, so you only
specify what changes.

The same override pattern works for **entrypoints**:

- The `entrypoints` section of the suite-level `dojo.yaml` defines shared
entrypoint config (type, path, method, headers).
- A test-level `dojo.yaml` overlays only the fields that differ. The suite
entrypoint is copied first, then the test YAML is merged on top.

This is especially useful when multiple tests hit the same endpoint but differ
only in a single header (e.g., HMAC signatures, API keys):

Suite-level `dojo.yaml` (shared base):

```yaml
entrypoints:
  secure:
    type: http
    method: GET
    path: "/secure"
```

Test-level `test_secure_header_valid/dojo.yaml` (only the diff):

```yaml
entrypoints:
  secure:
    headers:
      X-Api-Key: "test_secret_key"
```

Test-level `test_secure_header_invalid/dojo.yaml`:

```yaml
entrypoints:
  secure:
    headers:
      X-Api-Key: "wrong_key"
```

Both tests reference the same entrypoint name in their `.plan`:

```text
Perform -> entrypoints/secure -> ExpectStatus: "200"
```

Everything else is inherited from the suite-level base.

For API overrides, if every test shares the same API URL and mode but
one test needs a different `default_response`, that test only carries:

```yaml
apis:
  my_api:
    default_response:
      code: 200
      body: '{"messages":[{"id":"wamid.update_reply"}]}'
```

Everything else is inherited from the suite-level API config in `dojo.yaml`.

### Deep Merge (Fixture Inheritance)

When the **same fixture filename** exists at both the suite level and the test
level, Dojo deep-merges them: the suite file acts as the base and the test file
is merged on top. Only the fields that differ need to appear in the test copy.

- Nested JSON objects are merged recursively.
- Arrays and scalar values in the test file replace the suite values entirely.
- If either file is not a JSON object, the test file wins outright.

**Example: `gemini_request.json`**

Suite-level (shared across all tests):

```json
{
  "generationConfig": {
    "temperature": 0.7,
    "topP": 0.95,
    "topK": 40,
    "maxOutputTokens": 1024,
    "responseMimeType": "application/json"
  },
  "safetySettings": [
    { "category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_MEDIUM_AND_ABOVE" },
    { "category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "BLOCK_MEDIUM_AND_ABOVE" }
  ]
}
```

Test-level (`test_user_deactivate/gemini_request.json` -- only what differs):

```json
{
  "contents": [
    { "role": "user", "parts": [{ "text": "Delete my account" }] }
  ],
  "systemInstruction": {
    "parts": [{ "text": "You are a routing assistant. Resolve queries for user usr_55." }]
  }
}
```

At runtime, Dojo produces a **resolved fixture** containing all four top-level
keys: `generationConfig` and `safetySettings` from the suite base,
`contents` and `systemInstruction` from the test overlay. The test author only
wrote the 8 lines that are unique to this test case.

The same pattern works for any fixture. The suite-level `whatsapp_request.json`
carries the shared envelope fields (`messaging_product`, `recipient_type`,
`type`), and each test adds only the recipient-specific fields (`to`,
`text.body`).

---

### Suite Directory Structure

A Dojo suite is a directory containing a `dojo.yaml` file, an optional `startup.plan`,
and one or more `test_`* subdirectories. Here is the real example suite shipped
with the project:

```text
tests/blackbox/
  dojo.yaml                              # SUT command, concurrency, APIs, entrypoints
  seed/
    schema.sql                           # Shared DDL, run before all tests
  gemini_request.json                    # Suite-level fixture (deep-merge base)

  test_user_register/
    user_register.plan                   # The plan (pure logical intent)
    incoming.json                        # Perform payload (webhook body)
    gemini_request.json                  # Test fixture (deep-merged with suite)
    gemini_response.json                 # Mock response returned to SUT
    postgres_request.sql                 # Expected SQL query

  test_user_update/
    user_update.plan
    incoming.json
    gemini_request.json
    gemini_response.json
    postgres_request.sql
    dojo.yaml                            # Test-specific API/entrypoint overrides
    seed/
      seed.sql                           # Test-specific seed data
```

Key observations:

- Every `test_*` directory has exactly **one** `.plan` file.
- Fixture files like `gemini_request.json` appear at both suite and test level. The suite copy is the base; the test copy carries only the diff.
- `test_user_update` has a local `dojo.yaml` -- it overrides the suite config for that single test.
- `seed/` directories can exist at suite level (shared schema) and test level (test-specific data).
- If any **per-test** `seed/*.sql` fails against **live** Postgres, Dojo marks **every** test in the suite as failed after the run (tests that already finished with pass are flipped with a `suite aborted because seeding failed…` reason). Per-test seeds run **serially** so concurrent workers do not execute SQL scripts against the same database at the same time.
- Set `strict_duplicate_expects: true` in suite `dojo.yaml` to enforce the duplicate expected-request check even when `concurrency: 1` (by default that check runs only when `concurrency > 1`).

---

### The `.plan` DSL

Because all technical configuration lives in `dojo.yaml` and fixtures are
discovered by convention, `.plan` files read like pure intent.

**Syntax:** `Action -> Target -> Clause -> Clause`

Every `.plan` **must** begin with a `Perform` action to trigger the SUT.

### Multi-phase plans: `Perform -> postgres` and `Perform -> wait`

Each additional `Perform` line starts a **new phase** after the previous phase
finishes (all `Expect` lines for the HTTP trigger are satisfied, then any
`Perform -> postgres` / `Perform -> wait` steps run in order).

- **`Perform -> postgres`** runs a SQL fixture against **live** Postgres (see
  suite `dojo.yaml` for a live `postgres` API). Use `Query:` / `Expect:` clauses
  or the positional forms documented in the example suite
  (`example/tests/blackbox/test_perform_postgres/`). For **`Perform -> wait`**
  only, see the smaller example **`example/tests/blackbox/test_perform_wait/`**
  (`wait_example.plan` uses `Duration: 1ms` between the HTTP phase and a DB check).

- **`Perform -> wait`** pauses the test for a **Go duration** (e.g. `500ms`,
  `2s`). Supply it as `Duration: 500ms` or as a single positional token
  (`Perform -> wait -> 250ms`). The duration must be positive. There must be
  **no** `Expect` lines in a wait phase (only the wait line for that phase).
  The pause respects test cancellation (`context`).

### Request Matching: Normalized Full Equality

Dojo correlates intercepted SUT traffic to active tests using **normalized full
equality** between the resolved expected request fixture and the actual payload
on the wire. There is no separate correlation config or routing key.

When the SUT makes an outbound call to an API, Dojo:

1. Normalizes the actual request payload.
2. Compares it for exact equality against every active test's resolved expected
  request for that API.
3. A single match means that request belongs to that test.

## Walk-through: `test_user_deactivate`

Here is a concrete end-to-end flow for one test in the example suite:

1. **Boot:** Dojo reads `dojo.yaml`, discovers all `test_*` directories, and
   loads suite-level API configs.
2. **Fixture resolution:** For `test_user_deactivate`, Dojo finds
  `gemini_request.json` at both suite and test level. It deep-merges them:
   `generationConfig` + `safetySettings` from the suite, `contents` +
   `systemInstruction` from the test. Same for `whatsapp_request.json`.
3. **Trigger:** Dojo sends the contents of `incoming.json` to the SUT's
  `/trigger` endpoint (the `Perform` step).
4. **SUT issues a DELETE query:** The SUT connects to Postgres through Dojo's
  proxy and sends `DELETE FROM users WHERE ...`. Dojo normalizes the SQL and
   matches it to `test_user_deactivate`'s expected `postgres_request.sql`
   fixture.
5. **SUT calls Gemini:** The SUT sends an HTTP request to the Gemini API
  endpoint. Dojo normalizes the JSON body, matches it to the resolved
   (deep-merged) `gemini_request.json`, and returns `gemini_response.json` as
   the mock response.
6. **SUT calls WhatsApp:** The SUT sends an HTTP request to the WhatsApp API.
  Dojo matches the normalized body to the resolved `whatsapp_request.json` and
   returns the `default_response` configured for that API in `dojo.yaml`.
7. **Verdict:** All three expectations are fulfilled. The test passes.

---

## Advanced Features

### Startup Plan (`startup.plan`)

A suite may include a file named **`startup.plan`** in the suite directory (next to `dojo.yaml`). It is optional.

#### Purpose

Some systems call external APIs during process startup (cache warm-up, feature flags, license checks). Normal test plans begin with a **`Perform`** that drives the SUT, so they cannot observe traffic that happens before the first trigger.

The startup plan runs **after** the HTTP (and Postgres) proxies are up and **`API_*_URL` environment variables** are set, but **before** any test from `RunSuite` executes:

1. Proxies start; suite seeds run if configured.
2. If `startup.plan` exists, Dojo registers a synthetic active test (`startup`) and wires its expectations the same way as a normal test plan.
3. The SUT process starts (`sut_command`).
4. Dojo waits until the SUT's HTTP listener is ready (same TCP readiness as today).
5. Dojo waits until every **`Expect`** in `startup.plan` is satisfied (or times out / SUT crash).
6. The `startup` registration is removed; **`RunSuite` begins** -- no folder test runs until step 5 succeeds.

So: **all normal tests run only after the startup plan completes.**

Before `StartProxies`, the CLI **preflight** pass (after `LoadWorkspace`) also validates `startup.plan` the same way the engine wires fixtures, and validates every test plan’s `Expect` lines (known API, `MaxCalls`, and `Evaluate Response` vs configured eval rules).

#### Syntax

- **Only** `Expect` lines are allowed (no `Perform`, no `Perform -> postgres`, etc.).
- Same `Expect` grammar as test plans, for example:
  ```text
  Expect -> gemini -> Request: startup_gemini_request.json -> Respond: startup_gemini_response.json
  ```
- Fixture paths resolve relative to the **suite directory** (e.g. `example/tests/blackbox/`), same as suite-level assets.

#### Matching & Timeouts

Request matching uses the same rules as regular tests (JSON subset match for HTTP, normalized SQL for Postgres). Duplicate-request validation across **named tests** does not include the startup phase; still use a **unique** expected payload per API for the startup probe so it does not collide with a real test's first expectation if you ever merged logic.

Per-API `timeout` in `dojo.yaml` applies to startup expectations the same way as for tests. Increase `expect` or the Gemini API timeout if startup work is slow.

#### Example

See **`example/tests/blackbox/`**:

- `startup.plan` -- one Gemini expectation.
- `startup_gemini_request.json` / `startup_gemini_response.json` -- fixtures.
- **`example/sut/main.go`** -- after `ListenAndServe` is running, a background goroutine waits for `/health` then POSTs the probe body to `API_GEMINI_URL`.

---

### AI-Augmented Evaluation

Standard assertions fail when your SUT interacts with LLMs or returns
non-deterministic data. Dojo solves this with the `Evaluate Response` directive
in the `.plan` file.

When triggered, Dojo captures the SUT's network payload, compiles the Markdown
rules found in the test's `eval.md` (and the suite-level `eval.md`), and sends
them to an LLM for grading.

**Example `eval.md`:**

```markdown
The payload must contain a valid JSON object.
The `status` field must be "success".
The `message` field must be a polite greeting in Spanish.
Do not fail the test due to varying timestamps or unique IDs.
```

A test `eval.md` whose content starts with `+` appends to the suite-level eval
rather than replacing it.

---

### Timeouts

Dojo provides two DSL-aligned timeout keys in `dojo.yaml`, plus per-API
overrides.

#### Global timeouts in `dojo.yaml`

```yaml
timeouts:
  perform: 5s
  expect: 2s
```


| Key       | Default | Controls                                                                                                                                        |
| --------- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `perform` | `5s`    | How long the `Perform` HTTP trigger call to the SUT can take. Also used as the upstream timeout when proxying to real APIs in `"mode": "live"`. |
| `expect`  | `2s`    | How long Dojo waits for each `Expect` line to be fulfilled before marking it as timed out.                                                      |


The `expect` default of 2s is aggressive by design -- it catches broken tests
fast instead of hanging forever. Each expectation times out independently: a
missed expect fails with a timeout error while other expectations continue to
be evaluated, so you see every failure in a single run.

#### Per-API timeout override

The `timeout` field on any API config overrides the global `expect` timeout for
that specific API. This is the primary mechanism for accommodating slow
dependencies:

```yaml
apis:
  gemini:
    mode: live
    url: "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"
    timeout: 30s
```

#### Per-test override via test-level `dojo.yaml`

Since test-level `dojo.yaml` overlays merge onto the suite config, you can
override the timeout for a single test without affecting others:

```yaml
apis:
  gemini:
    timeout: 60s
```

Place this in `test_complex_prompt/dojo.yaml` and only that test gets
60s for Gemini calls.

#### Resolution order (first non-zero wins)

1. Per-API `timeout` in a test-level `dojo.yaml`
2. Per-API `timeout` in the suite-level `dojo.yaml`
3. `expect` in `dojo.yaml` `timeouts`
4. Built-in default: `2s`

#### Long-running LLM queries

For prompt regression tests that call real LLMs (not mocks), set `timeout` on
the LLM API config:

```yaml
apis:
  gemini:
    mode: live
    timeout: 30s
```

Or raise the global `expect` timeout in `dojo.yaml` if all APIs in the suite
are slow:

```yaml
timeouts:
  expect: 30s
```

---

### Environment Files

Each suite can have `.env` and `.env.local` files in its directory. Dojo loads
them (in that order) into both the Dojo process and the SUT process before
anything else runs.

- **`.env`** — committed to git. Use for URL mappings and test constants.
- **`.env.local`** — gitignored. Use for real API keys and secrets.

`.env.local` values override `.env`. Values support `$VAR` expansion against
Dojo's injected env vars (e.g. `DATABASE_URL=$API_POSTGRES_URL`).

Example `.env`:

```
GEMINI_BASE_URL=$API_GEMINI_URL
DATABASE_URL=$API_POSTGRES_URL
ENVIRONMENT=production
```

Example `.env.local`:

```
GEMINI_API_KEY=your-actual-key
```

### Env Var Expansion in Mock Responses

Mock response bodies support `$VAR` expansion using the process environment.
This is most useful for referencing other API proxy URLs inside a response.

For example, a media-lookup API whose response points back to a download API:

```yaml
apis:
  media_lookup:
    mode: mock
    default_response:
      code: 200
      body: '{"url": "$API_MEDIA_DOWNLOAD_URL/file.jpg"}'
```

At runtime, Dojo replaces `$API_MEDIA_DOWNLOAD_URL` with the actual proxy
address (e.g. `http://127.0.0.1:54321/media_download`). This lets you chain
mock APIs: one returns a URL, and the SUT follows that URL back through another
Dojo mock, all without hardcoding addresses.

Expansion uses Go's `os.ExpandEnv` and applies to both `default_response.body`
and per-expectation `Respond:` bodies.

### Binary File Responses

Mock APIs can serve binary files (images, PDFs, etc.) using `file` and
`content_type` in `default_response`:

```yaml
apis:
  media:
    mode: mock
    default_response:
      code: 200
      file: photo.jpg
      content_type: image/jpeg
```


| Field          | Description                                                                                                  |
| -------------- | ------------------------------------------------------------------------------------------------------------ |
| `file`         | Path to a binary file. Resolved relative to the API config's directory (test dir first, suite dir fallback). |
| `content_type` | `Content-Type` header for the response (defaults to `application/json` when omitted).                        |


Binary file payloads skip `$VAR` expansion to avoid corrupting binary data.

Place the API override and the binary file together in the test directory:

```text
test_media_process/
  dojo.yaml                      # {"apis": {"media": {"default_response": {"file": "photo.jpg", "content_type": "image/jpeg"}}}}
  photo.jpg                      # Served as the mock response body
  media_process.plan
```

Test-level API overrides apply even when the plan has no `Expect` clause for
that API -- Dojo uses the override for mock responses whenever that test is the
sole active test.

### MaxCalls (Variable Repeat Expectations)

When a SUT makes a variable number of calls to the same API -- LLM tool-calling
loops, retry patterns, pagination -- use `MaxCalls:` on an `Expect` line to
allow it to match up to N times before it is fulfilled.

```text
Perform -> POST /webhook -> Payload: incoming.json

Expect -> gemini -> Request: tool_call.json -> MaxCalls: "5"
Expect -> gemini -> Request: final_call.json -> Respond: final_response.json
Expect -> whatsapp
```

**Semantics: greedy with lookahead.** The engine consumes up to N matches for
the current expectation, but moves on early if an incoming request matches the
*next* expectation instead. In the example above, if the SUT makes 3 tool calls
then sends the final call, Dojo fulfills the first expectation at 3 (not 5) and
advances to the second.

**Constraint:** `MaxCalls:` cannot be combined with `Respond:`. It is designed
for `live` APIs where Dojo proxies to the real upstream and observes
responses -- not for mocked APIs where Dojo provides canned responses.

---

### Tracing & LLM Usage

Use the `--trace` flag to log all HTTP and Postgres traffic flowing through
Dojo's proxies. Each log line is correlated with the matched test ID and
truncated to 500 characters for readability:

```bash
dojo --trace ./example/tests/blackbox
```

Trace output includes:
- **HTTP requests** -- API name, URL path, and request body.
- **HTTP responses** -- mock or live response body, correlated by test ID.
- **Postgres queries** -- Query, Parse, and Bind messages with the SQL text.
- **Postgres responses** -- live wire-protocol response, correlated by test ID.

All trace output goes to stderr via `slog` and does not affect stdout formats
(`--format json`, `--format jsonl`).

#### LLM token tracking

Dojo automatically parses token usage from **live** HTTP API responses (see
`internal/engine/match_llm_usage.go`). Recognized shapes:

| Source | JSON location | Notes |
|--------|---------------|--------|
| **Google Gemini** | Top-level `usageMetadata` | `promptTokenCount`, `candidatesTokenCount`, `totalTokenCount`, `cachedContentTokenCount`, `toolUsePromptTokenCount`, `thoughtsTokenCount`, etc. Parsed **before** top-level `usage` so Gemini bodies are never misclassified. |
| **OpenAI Chat Completions** | `usage` | `prompt_tokens`, `completion_tokens`, `total_tokens`, plus nested `prompt_tokens_details` / `completion_tokens_details` (e.g. `cached_tokens`, `reasoning_tokens`, `audio_tokens`, predicted-output token fields). |
| **OpenAI Responses-style** | `usage` | When `usage` has `input_tokens` / `output_tokens` and **no** `prompt_tokens`, counts map like Anthropic (same branch). If both `prompt_tokens` and `input_tokens` appear, Chat Completions fields win. |
| **Anthropic Messages** | `usage` | `input_tokens` → prompt, `output_tokens` → completion, `cache_creation_input_tokens`, `cache_read_input_tokens`. |
| **Nested** | `response.usage` | Same rules as top-level `usage` when present. |

Mock LLM fixtures may include the same usage blocks; those counts are aggregated
the same way.

Counts are **summed** for every matching response in a test. Per-API totals are
stored under `llm_usage_by_api` (API name keys) when at least one API has
non-zero usage.

**Derived rates** (on `llm_usage_derived`, omitted when not applicable):

- `prompt_cache_hit_rate` — `sum(cached_prompt_tokens) / sum(prompt_tokens)` when `sum(cached_prompt_tokens) > 0` and `sum(prompt_tokens) > 0`. `cached_prompt_tokens` comes from OpenAI `cached_tokens` and Gemini `cachedContentTokenCount`.
- `cache_read_input_rate` — `sum(cache_read_input_tokens) / sum(prompt_tokens)` when `sum(cache_read_input_tokens) > 0` and `sum(prompt_tokens) > 0` (Anthropic cache reads vs billable input).

Numerators and denominators are also emitted on `llm_usage_derived` for CI.

In **console** output (default), LLM token counts are **not** printed on stdout
(only `PASS` / `FAIL` lines and the results summary). Pass **`--llm-usage`** for
tabular per-test and suite breakdowns (core metrics, extra counters, derived rates, and
per-API rows). JSON and jsonl output are unchanged and always include full
`llm_usage` fields when usage was observed. The CLI also recognizes
`--llm-usage` anywhere on the command line (e.g. `dojo run ./suite --llm-usage`
or `dojo run --llm-usage ./suite`), because the Go `flag` parser otherwise stops
at the first non-flag token.

```
PASS  test_intent_agent  (1.2s)

      --- LLM (test_intent_agent) ---
      ┌────────┬───────┬────────┬────────┬──────────┐
      │ API    │ Input │ Cached │ Output │ Thinking │
      ├────────┼───────┼────────┼────────┼──────────┤
      │ gemini │ 3000  │ 0      │ 847    │ 0        │
      └────────┴───────┴────────┴────────┴──────────┘
```

Columns are **Input** (prompt minus cached minus cache-read), **Cached** (cached
prompt plus cache-read), **Thinking** (reasoning plus thoughts), and **Output**
(billable completion-side tokens: when Gemini-style `thoughtsTokenCount` is
present, candidates plus thinking; otherwise OpenAI-style nested counts subtract
thinking from completion). Extra counters and derived rates print as bullets
under the table.

In **JSON** / **jsonl** output, each test result may include `llm_usage` (all
summed counters), `llm_usage_by_api`, and `llm_usage_derived`. Tracking is
automatic; fields are omitted when no usage was observed. **Streaming/SSE**
usage (final chunk only) is not parsed yet.

## Outputs and Artifacts

By default Dojo prints results to the console. Pass `--output <dir>` to write
machine-readable artifacts:

```text
<output-dir>/
  summary.json             # Structured results for AI agents and CI (total, passed, failed, per-test details)
  summary.md               # Markdown summary for humans (results table, failure details)
```

Use `--format json` for a single JSON blob on stdout, or `--format jsonl` to
stream one JSON object per test result as it completes.

If the SUT process crashes (exit code != 0), Dojo propagates the error to all
in-flight tests and reports each as failed with the crash reason.

---

## Documentation

- **[AGENTS.md](AGENTS.md)** -- full agent and contributor instructions (TDD, architecture, validation commands).
- **[docs/dojo-skill.md](docs/dojo-skill.md)** -- user-facing guide for writing `.plan` suites, fixtures, `startup.plan`, and consumer layouts (also usable as a **Cursor Agent Skill**; see file header for install path).

---

## Repository layout


| Path                      | Purpose                                                                           |
| ------------------------- | --------------------------------------------------------------------------------- |
| `cmd/dojo/main.go`        | CLI entrypoint                                                                    |
| `internal/engine/`        | Suite run, startup phase, matching                                                |
| `internal/workspace/`     | Load `dojo.yaml`, APIs, plans, `startup.plan`                                     |
| `example/sut/`            | Example HTTP SUT used by `example/tests/blackbox`                                 |
| `example/tests/blackbox/` | Integration-style example tests + `startup.plan`                                  |
| `docs/dojo-skill.md`      | Long-form suite-authoring reference (shipped; Cursor skill install noted in-file) |

---

## License

Licensed under the [MIT License](LICENSE).


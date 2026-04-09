# Project Dojo

**A Black-Box Contract Engine for Agentic Software Development**

Dojo is a declarative testing engine built in Go. It acts as a transparent
Man-in-the-Middle proxy between your Software Under Test (SUT) and its
dependencies. By intercepting HTTP traffic and raw Postgres queries, Dojo lets
you assert, mock, or AI-evaluate your application's behavior entirely from the
outside -- without touching a single line of application code.

---

## Built for the Era of AI Coding Agents

Traditional unit tests are implementation-heavy and tightly coupled to code
structure. When an AI coding agent refactors your codebase, internal mocks and
unit tests break, grinding autonomous development to a halt. Humans end up
spending more time fixing the agent's tests than the agent spent writing the
feature.

**Dojo solves the AI coding bottleneck.** Because Dojo tests your application
as a complete Black Box -- validating only the raw database queries and HTTP
requests it puts on the wire -- your tests become **implementation-agnostic
contracts**.

* **You act as the Architect:** You write the `.plan` file and configure the
  `apis/` folder to define *what* the app should do and what side effects it
  must produce.
* **The Agent acts as the Builder:** Your AI coding agent figures out *how* to
  code it. It can use any language, any design pattern, and refactor endlessly.
* **Dojo acts as the Judge:** If the suite passes, the agent's code is correct.
  Dojo is the deterministic guardrail for autonomous code generation.

---

## Unlocking New Engineering Use Cases

By decoupling the test from the implementation, Dojo enables testing workflows
that are traditionally nightmares to maintain:

* **Prompt Regression and Agent Tool-Use:** Stop mocking your LLM agent's
  internal reasoning. Send a prompt via Dojo and listen on the wire to see if
  the agent actually triggered the correct webhook or Postgres `UPDATE`.
* **AI-to-AI Evaluations:** Upgrading to a new LLM model? Pump 500 historical
  chat logs through Dojo and use the `Evaluate Response` directive to have a
  second LLM grade the new model's output against your brand guidelines.
* **The "Strangler Fig" Migration:** Rewriting a 10-year-old Python backend
  into Go? Point the exact same Dojo suite at both apps. If the Go app produces
  the exact same Postgres `INSERT` statements and HTTP responses, your migration
  is bug-for-bug compatible.
* **Chaos Engineering:** Configure a mock in your `apis/` folder to force the
  Stripe API to timeout or return a `502`. Verify on the wire that your app
  caught the error and rolled back the database transaction instead of crashing.

---

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

## Convention Over Configuration

Dojo separates **what to test** from **how to connect**. Technical wiring --
URLs, protocols, timeouts -- lives in `apis/*.json`. The `.plan` file names
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

* `apis/*.json` at the **suite level** defines shared config (URLs, mode,
  timeouts, default responses).
* `test_*/apis/*.json` at the **test level** overlays only the fields that
  differ. The suite config is copied first, then the test JSON is applied on
  top, so you only specify what changes.

For example, if every test shares the same WhatsApp API URL and mode but one
test needs a different `default_response`, that test only carries:

```json
{
  "default_response": {
    "code": 200,
    "body": "{\"messages\":[{\"id\":\"wamid.update_reply\"}]}"
  }
}
```

Everything else is inherited from the suite-level `apis/whatsapp.json`.

### Deep Merge (Fixture Inheritance)

When the **same fixture filename** exists at both the suite level and the test
level, Dojo deep-merges them: the suite file acts as the base and the test file
is merged on top. Only the fields that differ need to appear in the test copy.

* Nested JSON objects are merged recursively.
* Arrays and scalar values in the test file replace the suite values entirely.
* If either file is not a JSON object, the test file wins outright.

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

## Suite Directory Structure

A Dojo suite is a directory containing a `dojo.config` file, an `apis/` folder,
and one or more `test_*` subdirectories. Here is the real example suite shipped
with the project:

```text
tests/blackbox/
  dojo.config                            # SUT command, concurrency settings
  apis/
    gemini.json                          # mode: mock, URL path
    postgres.json                        # mode: live, connection string
    whatsapp.json                        # mode: mock, inline default_response
  entrypoints/
    webhook.json                         # How Dojo triggers the SUT (JSON payload)
    upload.json                          # How Dojo triggers the SUT (binary payload)
  seed/
    schema.sql                           # Shared DDL, run before all tests
  gemini_request.json                    # Suite-level fixture (deep-merge base)
  whatsapp_request.json                  # Suite-level fixture (deep-merge base)

  test_user_register/
    user_register.plan                   # The plan (pure logical intent)
    incoming.json                        # Perform payload (webhook body)
    gemini_request.json                  # Test fixture (deep-merged with suite)
    gemini_response.json                 # Mock response returned to SUT
    whatsapp_request.json                # Test fixture (deep-merged with suite)
    postgres_request.sql                 # Expected SQL query

  test_user_lookup/
    user_lookup.plan
    incoming.json
    gemini_request.json
    gemini_response.json
    whatsapp_request.json
    postgres_request.sql
    seed/
      seed.sql                           # Test-specific seed data

  test_user_update/
    user_update.plan
    incoming.json
    gemini_request.json
    gemini_response.json
    whatsapp_request.json
    postgres_request.sql
    apis/
      whatsapp.json                      # Test-specific API config override
    seed/
      seed.sql

  test_user_deactivate/
    user_deactivate.plan
    incoming.json
    gemini_request.json
    gemini_response.json
    whatsapp_request.json
    postgres_request.sql
    seed/
      seed.sql

  test_image_upload/
    image_upload.plan
    image.jpg                            # Binary payload sent to the SUT
    gemini_request.json
    gemini_response.json
```

Key observations:

* Every `test_*` directory has exactly **one** `.plan` file. Name it whatever
  you want.
* Fixture files like `gemini_request.json` appear at both suite and test level.
  The suite copy is the base; the test copy carries only the diff.
* Only `test_user_update` has a local `apis/whatsapp.json` -- it overrides the
  suite WhatsApp config for that single test.
* `seed/` directories can exist at suite level (shared schema) and test level
  (test-specific data).
* `test_image_upload` demonstrates binary fixture payloads -- the `.plan` uses
  `Payload: image.jpg` and Dojo sends the raw JPEG bytes to the SUT.

---

## The `.plan` DSL

Because all technical configuration lives in `apis/` and fixtures are
discovered by convention, `.plan` files read like pure intent.

**Syntax:** `Action -> Target -> Clause -> Clause`

Every `.plan` **must** begin with a `Perform` action to trigger the SUT.

### Example: `user_deactivate.plan`

```text
Perform -> entrypoints/webhook -> Payload: incoming.json

Expect -> postgres -> Request: postgres_request.sql
Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
Expect -> whatsapp -> Request: whatsapp_request.json
```

Line by line:

1. **Perform** triggers the SUT's `/trigger` endpoint with the contents of
   `incoming.json` from the test directory.
2. **Expect postgres** declares that the SUT must issue a SQL query matching
   the contents of `postgres_request.sql`.
3. **Expect gemini** declares that the SUT must call the Gemini API with a
   payload matching `gemini_request.json` (resolved via deep merge from suite +
   test). When matched, Dojo returns `gemini_response.json` as the mock
   response.
4. **Expect whatsapp** declares that the SUT must call the WhatsApp API
   with a payload matching `whatsapp_request.json` (resolved via deep merge).
   When no `Respond:` clause is given, the mock response comes from the
   suite-level `apis/whatsapp.json` `default_response`.

### Example: `user_register.plan`

```text
Perform -> entrypoints/webhook -> Payload: incoming.json

Expect -> postgres -> Request: postgres_request.sql
Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
Expect -> whatsapp -> Request: whatsapp_request.json
```

The structure is identical. Only the fixture contents differ -- different
`incoming.json`, different `gemini_request.json` overlay, different SQL in
`postgres_request.sql`. The `.plan` stays clean.

---

## Request Matching: Normalized Full Equality

Dojo correlates intercepted SUT traffic to active tests using **normalized full
equality** between the resolved expected request fixture and the actual payload
on the wire. There is no separate correlation config or routing key.

When the SUT makes an outbound call to an API, Dojo:

1. Normalizes the actual request payload.
2. Compares it for exact equality against every active test's resolved expected
   request for that API.
3. A single match means that request belongs to that test.

### Normalization Rules

* **Postgres (SQL):** Collapse all whitespace to single spaces. Strip trailing
  semicolons. This means `SELECT  user_id  FROM users ;` and
  `SELECT user_id FROM users` are identical.
* **HTTP / JSON:** If the body is valid JSON, canonicalize it (sorted keys,
  compact encoding). Otherwise, collapse whitespace on the raw text.

### Uniqueness Constraint

Because matching relies on equality, **no two tests in a suite may share the
same normalized expected request for the same API**. The workspace loader
validates this at boot and rejects duplicates with an error. This guarantee
means a match is always unambiguous.

---

## AI-Augmented Evaluation

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

## Walk-through: `test_user_deactivate`

Here is a concrete end-to-end flow for one test in the example suite:

1. **Boot:** Dojo reads `dojo.config`, discovers 5 tests (`test_user_register`,
   `test_user_lookup`, `test_user_update`, `test_user_deactivate`,
   `test_image_upload`), and loads suite-level API configs from `apis/`.

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
   returns the `default_response` defined in `apis/whatsapp.json`.

7. **Verdict:** All three expectations are fulfilled. The test passes.

---

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

## Getting Started

### Prerequisites

* Go 1.24+
* Docker (for integration tests and spinning up dependencies)
* `GEMINI_API_KEY` exported in your environment (for AI evaluations)

### Running a Suite

```bash
# From the project root:
go run cmd/dojo/main.go ./example/tests/blackbox

# Or after building:
dojo ./example/tests/blackbox
dojo --format json -o results/ ./example/tests/blackbox
```

Dojo will:

1. Read `dojo.config` and configure environment variables to route SUT traffic
   through Dojo's local proxies.
2. Boot your application as a child process (when `sut_command` is set).
3. Spin up concurrent test workers (up to the configured `concurrency` limit).
4. Execute all `Perform` triggers and wait for `Expect` matches.
5. Tear down the application and print the verdict (pass `--output` to write
   `summary.json` and `summary.md` to disk).

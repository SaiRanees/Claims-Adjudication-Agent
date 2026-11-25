# claims-agent

An AI agent that adjudicates Indian health insurance reimbursement claims, starting with Arogya Sanjeevani on the Go Digit wording (UIN GODHLIP24179V022324). The agent gathers and checks facts from the claim documents, a deterministic rules engine computes every amount, and people review anything flagged and make every denial.

- Full design: `docs/plan/architecture.md`. Read the relevant section before changing anything design-level.
- Current checklist: `docs/plan/week-3.md`. Tick items there as they're finished.
- Decisions: `docs/adr/`. A change that contradicts an ADR needs a new ADR first.

## Rules that are never broken

1. Money is `int64` paise everywhere: `bigint` in SQL, `int64` in Go, an integer in the API. Percentages are integer basis points (5% is 500). No floating point on the money path. (ADR 0005)
2. `internal/rules` is pure: no I/O, no `time.Now`, no randomness, no model calls. Dates come in as inputs. golangci-lint enforces this with depguard and forbidigo; never silence those rules. (ADR 0005)
3. Every message to a workflow (start, documents added, submitted, review decided) is an `outbox` row written in the same transaction as the change that causes it. The API never calls Temporal. (ADR 0003)
4. Only IDs, versions and small enums pass through Temporal: never document text, prompts or model output. Outbox payloads over 2 KB fail a CHECK constraint. (ADR 0004)
5. Workflow code is deterministic: no clock, randomness, network or goroutines except through the Temporal SDK. Activities are idempotent.
6. Models never produce amounts; the engine does. A denial always goes to a person.
7. Synthetic data only. Never add real personal data, or code, data or documents from any employer or client. (ADR 0006)
8. Golden cases are checked by hand. Never change a golden case's expected values or `evals/engine/golden-cases.csv`, and never skip or weaken the tests that compare them, to make a test pass; stop and report the diff.
9. Extraction is graded against generated truth and recorded model output. Never change a scenario spec, generated truth, a model cassette or an eval report by hand to improve a number, and never skip or weaken the checks that compare them. Truth changes only through a spec or the generator, a cassette only through a record run, and a report only through a run of the eval; stop and report the difference instead. (ADR 0014)

## Working here

- Keep each change small and reviewable, and explain the reasoning in your reply.
- Ask first before adding a dependency, changing an ADR-level decision, editing an applied migration, or deleting files.
- Before saying something works, run its check (see Commands) and report the output.
- Never commit `.env`, keys or tokens.

## Environment: native Windows

- Development happens natively on Windows, with no WSL or Linux distribution. Don't suggest installing one, and don't add Make or host-side shell scripts. (ADR 0007)
- Every project task runs through `go run ./cmd/dev <task>`, which needs only Go 1.27 and Docker Desktop and works in PowerShell, cmd and Git Bash alike. Add new tasks to `cmd/dev`, in Go.
- Shell scripts are only for inside Linux containers, such as `scripts/db-check.sh`, which `dev db-check` pipes into the Postgres container. Keep them POSIX `sh`.
- Use LF line endings; `.gitattributes` and `.editorconfig` enforce them. In Go, build paths with `filepath.Join` and never assume a POSIX shell.
- `go test -race` needs a C compiler on Windows. `dev test` runs without `-race` when there isn't one; CI always runs with it.
- golangci-lint and Redocly run from pinned Docker images through the dev tool, so there's nothing to install.

## Commands

| Task | Command |
| --- | --- |
| Start the stack (creates `.env` the first time, and adds variables new in `.env.example` later) | `go run ./cmd/dev up` |
| Stop, keep data / delete all data | `go run ./cmd/dev down` / `go run ./cmd/dev reset` |
| Apply migrations / show version | `go run ./cmd/dev migrate` / `go run ./cmd/dev migrate version` |
| Load synthetic data (safe to re-run) | `go run ./cmd/dev seed` |
| Check grants, the audit chain and constraints | `go run ./cmd/dev db-check` |
| psql with the services' privileges | `go run ./cmd/dev psql` |
| Add oapi-codegen as a Go tool (once) | `go run ./cmd/dev tools` |
| Regenerate `internal/api/apigen` | `go run ./cmd/dev gen` |
| Vet, lint and tests | `go run ./cmd/dev check` |
| The golden cases as pass, fail or unchecked (M1's gate) / lay out one case | `go run ./cmd/dev golden` / `go run ./cmd/evalrunner explain <case.yaml>` |
| Include the database, Temporal and SeaweedFS tests | automatic in `check` and `test` when the stack is up and `dev app`'s relay and worker aren't running (README, "First run") |
| Render the synthetic packets and self-check them | `go run ./cmd/datagen render -check` |
| Lint the API contract | `go run ./cmd/dev lint-api` |
| Format the Go code | `go run ./cmd/dev fmt` |
| Run api, relay and worker in containers | `go run ./cmd/dev app` |
| Print a bearer token for calling the API | `go run ./cmd/dev token [role] [subject]` |

`go run ./cmd/dev help` lists everything. Local addresses: Postgres `localhost:5432` (database `claims`), Temporal `localhost:7233` (namespace `claims`) with its UI on http://localhost:8233, SeaweedFS S3 on http://localhost:8333, Grafana on http://localhost:3000, OTLP on 4317 and 4318, the API on http://localhost:8080.

## Layout

- `cmd/api`, `cmd/relay`, `cmd/worker`: service entry points. `cmd/dev`: the task runner. `cmd/evalrunner`: the golden-case report and `explain`.
- `internal/api`: HTTP handlers, plus the middleware that checks every request against the contract (kin-openapi) and its bearer token before a handler runs. `internal/api/apigen` is generated by oapi-codegen v2.8.2 (strict server on `net/http`) from `api/openapi.yaml`. Never edit the generated file; change the spec and run `go run ./cmd/dev gen`.
- `internal/rules` (with `money`), `workflow`, `claims`, `documents`, `audit`, `policy`, `review`, `agent`, `llm`, `extraction`, `platform`: each `doc.go` states the package's job.
- `rulepacks`: one YAML rule pack per version of a policy wording, embedded and parsed into a registry. The embedding lives here because `internal/rules` can't import `io/fs`. A published pack's SHA-256 is pinned in `rulepacks_test.go`; never edit a published pack or its pin, add a new version in a new file.
- `internal/datagen`: the synthetic-packet generator (scenario specs, gopdf layouts with embedded Noto Sans, truth and the self-check). `cmd/datagen`: `render` and `golden-specs`. `evals/extraction`: the committed scenario specs (`scenarios/*.yaml`) and the golden-packet tests. Rendered packets go to `out/`, which git ignores.
- `evals/engine`: the engine's golden cases (`golden/*.yaml`, synthetic, arithmetic in comments), the loader and runner, `Evaluate` (the one definition of a pass), and `golden-cases.csv`, the figures checked by hand, compiled in.
- `db/init/01-roles.sql` (roles, read with psql `\getenv`), `db/migrations` (golang-migrate, forward-only), `db/seed/dev.sql` (synthetic), `scripts/db-check.sh`.

## Database conventions

- Two roles: `claims_owner` runs migrations and the seed; `claims_app` is what the services use.
- `claims_app` gets explicit grants per table and per column. `scripts/db-check.sh` holds the expected privilege list and fails on any difference, so change it in the same commit as the GRANTs.
- A new migration is `db/migrations/00000N_<name>.up.sql`, wrapped in `BEGIN`/`COMMIT`. Never edit one that has been applied anywhere; write a new one.
- IDs are UUIDv7, generated in Go before the insert. Tables have no ID defaults.
- `claims.version` goes up on every UPDATE (trigger). Update with `WHERE id = $1 AND version = $2` and treat zero rows as a conflict.
- Claim statuses: `draft` → `submitted` → `validating` → (`awaiting_documents`) → `extracting` → `adjudicating` → (`in_review`) → `decided` → `closed`. `rejected` means an unusable packet; `failed` means retries ran out and an operator must step in. `submitted_at` is set exactly when the status isn't `draft`, and `decided_at` exactly for `decided` and `closed`. CHECK constraints enforce both.
- `audit_events` is append-only (grants plus a trigger that blocks UPDATE, DELETE and TRUNCATE for every role) and hash-chained per claim. A foreign key from (`claim_id`, `prev_seq`, `prev_hash`) to (`claim_id`, `seq`, `hash`) makes the database reject gaps, forks and wrong links. `internal/audit` computes `hash` as SHA-256 over `prev_hash` and the event's canonical content. `details` holds IDs and amounts only.
- Idempotency: `idempotency_keys` is keyed by (principal, key) and stores the request hash and the first response. The same key with a different request is a 422; a duplicate still in flight is a 409. `claims_app` can't UPDATE `idempotency_keys`, so "in flight" is a transaction-scoped advisory lock (`internal/claims`). Only successful creates are stored, for 24 hours.
- Database tests run as `claims_app` and can't delete claims, so they use their own principal (`test-<uuid>`) and leave rows behind; `dev reset` clears them.

## API conventions

- `api/openapi.yaml` (OpenAPI 3.0.3) is the contract; change it first, then regenerate.
- Errors are `application/problem+json` (RFC 9457), with `code`, `errors[]` and `traceId`.
- Handlers not built yet return one sentinel error, and the strict handler's response-error hook maps it to a 501 problem.
- Bearer tokens are HS256 JWTs (ADR 0008): `sub` is the caller, `role` is submitter, reviewer or admin, and the audience is `claims-api`. Handlers read the caller from the context, never the token.
- Handlers check only what the contract can't express, such as discharge on or after admission. Field errors point into the body (`/claimedAmountPaise`) or at a parameter (`/header/Idempotency-Key`). Every problem's `traceId` is the request's trace ID.
- Sign upload URLs for `S3_PUBLIC_ENDPOINT` (reachable from the host), not `S3_ENDPOINT` (the in-network name).
- A browser's `EventSource` can't send an `Authorization` header, so the web app will use a fetch-based client for `/v1/claims/{claimId}/events`.

## Go conventions

- Logs: `log/slog` JSON from `platform.NewLogger`, which adds `trace_id` and `span_id` when you log with a context (`InfoContext` and the like). No `fmt.Print*`.
- Tracing: `platform.StartTelemetry` sets up OpenTelemetry (W3C `traceparent` only, no baggage), exporting over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Dial Temporal with `platform.DialTemporal`, which adds Temporal's tracing interceptor. Span names and attributes carry IDs, never content.
- Wrap errors with `%w`; check them with `errors.Is` and `errors.As`.
- A switch over a status lists every value; `default` doesn't count (the exhaustive linter).
- Tests are table-driven. The money package also gets a fuzz test.
- Format with `go run ./cmd/dev fmt` (gofmt and goimports, local prefix `github.com/SaiRanees/Claims-Adjudication-Agent`).

## Status, 24 November 2026

- Done: the dev task runner, Compose stack, Dockerfile, migration 000001 with seed and 29 database checks, OpenAPI v0 (12 operations), the CI workflow (with a Windows job) and ADRs 0001 to 0008.
- First run on a real machine (Windows 11, Docker Desktop), 11 November: `dev up` brings all four services up healthy, `migrate` applies 000001 and a second run changes nothing, `seed` is safe to re-run, `db-check` passes 29 of 29 on Postgres 18, `dev tools` and `dev gen` produce `internal/api/apigen/apigen.gen.go` (12 operations), and `dev check` passes.
- CI runs on pull requests and on pushes to main, not on other branch pushes. It has built all three service images.
- Session 4, the API skeleton: `GET /healthz`, idempotent `POST /v1/claims` (claim, outbox row and key in one transaction) and `GET /v1/claims/{claimId}` are built and tested, including against Postgres; the other nine operations answer 501. The api container was run against the stack and shut down cleanly on SIGTERM.
- Published at https://github.com/SaiRanees/Claims-Adjudication-Agent under the MIT licence. The Go module path matches the repo; `claims-agent` remains the local Compose project name.
- Session 5, the relay and first workflow: `cmd/relay` delivers the outbox to Temporal (`SKIP LOCKED`, per-claim order, backoff to a 5-minute cap) and `cmd/worker` runs the placeholder `ClaimWorkflow` on the `claims` task queue. With `dev up` and `dev app`, a new claim's `claim-<id>` shows as Running in the Temporal UI, and both services exit cleanly on SIGTERM. CI's Database job starts Temporal for the relay's end-to-end test (`TEST_TEMPORAL_ADDRESS`).
- Session 6, tracing: the API, relay and worker export traces to `otel-lgtm`. The API writes the request's `traceparent` on the outbox row, the relay continues it around each delivery, and Temporal's interceptor carries it into the workflow, so one create-claim trace runs from `POST /v1/claims` through the relay to the workflow and its activity in Tempo.
- Session 7, money and the gate: `internal/rules/money` has `Percent` (half-up) and `Allocate` (largest remainder), both computed in 128 bits with `math/bits`, so they're exact for every non-negative input; negative amounts, rates and weights are errors. Table tests and two fuzz tests check them against `math/big`. `api`, `relay` and `worker` restart on failure in Compose. `dev test` points the database and Temporal tests at the local stack by itself. The gate passed from a fresh clone (198 tests, none skipped), so week 1 is done. The one-minute demo moved to week 8, where M5 records the demo video.
- Week 2, M1 (the rules engine), is planned in `docs/plan/week-2.md`. ADRs 0009 (cap at the cover left before the co-payment) and 0010 (ICU charges are capped, never scaled; scaling by an ICU ratio is flagged) are accepted.
- Week 2, session 1, the rule pack: `rulepacks/arogya-sanjeevani/godigit-2024.yaml` holds every limit of the Go Digit wording, each citing its clause and page, and the order of the nine steps. `internal/rules` parses it strictly from bytes and selects a pack through the policy period's UIN. The pack is unpublished until the M1 gate pins its SHA-256.
- Week 2, session 2: three fixes from the session 1 review (ICU nursing and intensivist lines held to both caps with `bucket_segment_mismatch`, a `no-scaling` effect so a pack can drop ICU scaling, and `valid_from` 2023-04-01). Money gained checked `Add`, `Sum`, `Sub` and `MulDiv`; `Date` counts days and adds months. `Adjudicate` has its input and output types, input checks and a pass-through stub. The first 10 golden cases are in `evals/engine/golden/`, unchecked.
- Week 2, session 3: `golden-cases.csv` holds the independently checked figures for cases 01 to 21. The engine codes steps 1 to 3 (Annexure lists, unmodelled lines, daily caps and proportionate deduction), and every figure they produce matches all 24 golden cases. The proportionate-deduction ratio follows Note 2's rate per day, counting the days billed (case 22).
- Week 2, session 4: cases 22 to 24 are checked. A bucket's days billed are now the days its lines cover together, with `room_days_mismatch` judged per category (case 25: daily room lines). The engine codes every step but eligibility: sub-limits, the cover cap with `cover_exceeded` (ADR 0009), the co-payment, the bill discount, the ledger effects and `proposed_denial`. All 32 golden cases pass in full; 25 to 32 wait for their check.
- Week 2, session 5: cases 25 to 32 are checked. Two rule changes from the session 4 review: `no_room_charge` now means the bill has no room or ICU line at all for a segment of a stay longer than one day that still has a line to scale, and two room-rent lines covering the same day raise `room_days_mismatch`. The engine codes eligibility, so every step is coded: each tranche is tested against the waits from its own continuous-cover date, a wait of N runs through (D − 1 day) + N, and which waits an accident lifts is the pack's `waived_for_accident`. All 48 golden cases pass in full; 33 to 48 wait for their check.
- Week 2, session 6: cases 33 to 48 are checked. Cases 49 to 51 add `onset_before_cover` and judge Excl03(ii)'s 12 months at the admission. Every result carries `EngineVersion` (0.1.0), and a golden file pins the JSON of adjustments and flags. `FuzzAdjudicateLimits` fuzzes packs and facts near the `int64` limits, and `FuzzAdjudicateProperties` checks properties over generated claims. The property fuzzer found a higher sum insured lowering the payable, so ADR 0011 makes a segment's cap take its cut from the lines held to both caps first. That changes case 05's split between lines but not its ₹24,700. Rounding can still lower the payable by a paisa or so, so the property allows a bound derived per claim, which is zero for most claims (ADR 0011). Cases 01 to 48 are checked and pass, and 49 to 51 wait for their check.
- Week 2, session 7: cases 49 to 51 are checked. ADR 0012 puts the road-ambulance limit before the cataract and modern-treatment limits: in the old order a tied ambulance line took a share of the treatment's cut and was then cut again, so the claim fell below the treatment limit. The pack lists it first, the parser rejects the old order, and case 52 pins it; no checked figure moved, and `TestRoundingBound`'s third case now pays ₹38,000 at both sums insured with a bound of 0. `cmd/evalrunner engine` reports each case as pass, fail or unchecked through `evals/engine`'s `Evaluate`, the one definition of a pass the golden tests share, and `explain` lays out a case like architecture.md's worked example. The pack is published: `rulepacks` pins version 1's SHA-256, so any change to it means a new version in a new file. From a fresh clone, `dev golden` reports 52 cases checked and passing, and `dev check` passes. M1 is done.
- Week 3, the first half of M2 (intake and extraction), is planned in `docs/plan/week-3.md`. ADRs 0013 (PDFium in WebAssembly reads PDFs, pdfcpu writes them, gopdf draws the synthetic packets), 0014 (record and replay model calls with committed cassettes) and 0015 (the model copies printed values; code converts them) are accepted, with the plan, and its new dependencies are approved for the sessions it names.
- Week 3, session 1: `internal/audit` appends hash-chained events in the caller's transaction (SHA-256 over the previous hash and a canonical JSON encoding, never text read back from `jsonb`), and creating a claim writes `claim.created`. Every write for an existing claim locks its row first (`claims.lock`). `POST /v1/claims/{id}/documents` registers a file (submitter only, `draft` or `awaiting_documents`, at most 50 files) and returns a 15-minute `PUT` URL signed for its type and length by `documents.Objects` (aws-sdk-go-v2 `service/s3`, path-style, signed for `S3_PUBLIC_ENDPOINT`); the same pending file again is a 200. `…/complete` checks size and SHA-256 outside any transaction and records `uploaded` (with the `documentUploaded` outbox row) or `rejected`. Registering is rate-limited per caller (20, then one a second, 429 with `Retry-After`). CI's Database job runs SeaweedFS; the S3 tests read `TEST_S3_*`, which `dev test` fills from `.env`, and `TEST_S3_REQUIRED` turns their skip into a failure.
- Week 3, session 2: go-pdfium (WebAssembly on wazero, empty `FSConfig`, 256 MiB memory limit, killed at its deadline) and pdfcpu (stateless configuration) passed their proofs first, in `internal/documents`. Migration 000002 adds `accepted`, `checked_at`, `pdf_sha256`, the `type_mismatch`, `image_too_large` and `check_failed` reasons, constraints that follow the status and a forward-only trigger; db-check has 35 checks. `ClaimWorkflow` checks each `documentUploaded` file with `CheckDocument` (`documents.Checker`, committed by `claims.Store.RecordCheck`) and waits for every check before it passes the submission; a check out of retries after about an hour is `check_failed`. The worker now needs `DATABASE_URL` and the S3 settings. `POST /v1/claims/{id}/submit` needs one accepted file and none uploaded, under the claim's lock, and shares the rate limit. Fixtures for tests live in `internal/documents/fixtures`. The acceptance test (`internal/api/checks_stack_test.go`) drives its own claim's outbox to a worker on its own task queue.
- Week 3, session 3: gopdf v0.38.6 passed its proof (₹ and both digit groupings read back by PDFium, identical bytes in two processes) before any template. `internal/datagen` decodes scenario specs strictly (policy, cover and facts in the golden-case shape, presentation beside them), draws three hospital-bill layouts, a pharmacy bill and a receipt, and writes `truth.json` (generator version 1). A listed line prints the wording's item name, from a short Annexure A table, unless it has an explicit `matched_by` (cases 21 and 31). The self-check reads every page with PDFium (`PDFium.PageTexts`) and needs each line's strings on one text line of its page. The 52 golden specs, written once by `datagen golden-specs`, render byte-identically in two processes, and the engine on their truth gives every checked row of `golden-cases.csv`. gopdf always compresses the font subset, so session 4's pinned packet hashes guard `compress/flate`.

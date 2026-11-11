# Week 1 · Foundations

By the end of the week, one command starts the whole stack, and one claim travels from the API through the outbox into a running workflow, with a single trace end to end. The engine's money core starts alongside, because it needs no policy.

Progress, 11 November: the scaffold is built and checked, and the ticked items below are done. The first run on a real Windows machine has passed: the stack, the database checks, code generation and `go run ./cmd/dev check`. Still to do: the Go code (handlers, relay, workflow, tracing, the money package). Everything runs natively on Windows (ADR 0007).

## Decide first

- [x] First policy: Arogya Sanjeevani, using Go Digit's wording. A retail policy can come later as a second rule pack.
- [x] Pick the repo name and a licence: `SaiRanees/Claims-Adjudication-Agent`, which is also the Go module path, under the MIT licence. Redocly's `info-license` rule is back on. `claims-agent` stays as the local Compose project name, so existing volumes keep their data.

## Session 1: repo and tooling

- [x] Create a public GitHub repo and push the scaffold: [SaiRanees/Claims-Adjudication-Agent](https://github.com/SaiRanees/Claims-Adjudication-Agent). Its `go.mod` targets Go 1.27; the Anthropic Go SDK needs 1.25 or newer ([source](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go)).
- [x] Lay out the folders from the plan's repo structure.
- [x] Add a task runner with `up`, `down`, `migrate`, `gen`, `lint` and `test` tasks. It's a small Go program, `cmd/dev`, so it needs only Go and Docker and runs natively on Windows.
- [x] Add a golangci-lint config, `.gitignore`, `.editorconfig` and a README.

Done when `go run ./cmd/dev check` passes on the empty project.

Checked on 11 November on Windows, with the API server generated: `go vet`, golangci-lint v2.15.0 (0 issues), the Redocly lint and the tests all pass. The tests ran without `-race` because cgo is off on that machine; CI runs them with it.

## Session 2: local stack

- [x] Compose services in `compose.yaml` at the repo root: Postgres 18, SeaweedFS as the S3 store (`weed mini` creates the bucket on start, so there's no init job), the Temporal dev server, and `grafana/otel-lgtm`.
- [x] Run Temporal as `temporalio/temporal server start-dev --ip 0.0.0.0` on ports 7233 and 8233, and pre-register the namespace with `--namespace` ([source](https://docs.temporal.io/cli/setup-cli.md)).
- [x] Use `grafana/otel-lgtm` for observability: one development image with the OTEL Collector, Prometheus, Loki, Tempo and Grafana, listening on 4317 and 4318 with Grafana on 3000 ([source](https://github.com/grafana/docker-otel-lgtm)).
- [x] Health checks on Postgres, SeaweedFS and Temporal, and `depends_on` with `condition: service_healthy`.
- [x] A `.env.example`; real secrets are never committed.

Done when `go run ./cmd/dev up` brings everything up healthy, and the Temporal UI and Grafana open in a browser.

Checked on 11 November on Windows 11 with Docker Desktop: all four services report healthy, the Temporal UI shows the `claims` namespace, and Grafana lists its Prometheus, Loki, Tempo and Pyroscope data sources.

## Session 3: schema migrations

- [x] golang-migrate v4.20.1, forward-only, run from its Docker image so there's nothing to install.
- [x] Migration 0001 holds only the tables this week touches: `policies`, `policy_periods`, `members`, `claims`, `idempotency_keys`, `documents`, `outbox`, `audit_events`, `model_calls`. A claim starts in `draft`. The rest arrive with the milestone that needs them.
- [x] Money as `bigint` paise, CHECK constraints, foreign keys, and an index on unpublished outbox rows.
- [x] An app role with only INSERT and SELECT on `audit_events`, and column-level grants elsewhere. A trigger also blocks UPDATE, DELETE and TRUNCATE there for every role, the owner included.
- [x] A seed with one synthetic Arogya Sanjeevani policy (₹5,00,000 sum insured) and one member.

Done when migrations apply cleanly to an empty database, a second run changes nothing, and an `UPDATE` on `audit_events` as the app role fails.

Checked on Postgres 16, and again locally on Postgres 18 on 11 November: all three hold (a second `migrate` prints `no change`), and `go run ./cmd/dev db-check` runs 29 checks in all, this one included. CI repeats them on Postgres 18.

## Session 4: OpenAPI v0 and API skeleton

- [x] Write `api/openapi.yaml` for every endpoint in the plan: 12 operations, RFC 9457 problem details and the `Idempotency-Key` header. It passes Redocly's lint.
- [x] Generate the Go server. oapi-codegen v2.8.2's strict server on `net/http` is configured: run `go run ./cmd/dev tools` once, then `go run ./cmd/dev gen`.
- [ ] Implement `GET /healthz`, `POST /v1/claims` and `GET /v1/claims/{claimId}`. The other handlers return one sentinel error, and the strict handler's error hook turns it into a 501 problem: oapi-codegen's `net/http` output has no ready-made Unimplemented type.
- [ ] `POST /v1/claims` writes the claim and its outbox row in one transaction, and a repeated `Idempotency-Key` returns the first response.

Done when two identical create requests with the same key make one claim, and the spec passes a linter.

## Session 5: outbox relay and first workflow

- [ ] The relay takes unpublished rows with `SELECT … FOR UPDATE SKIP LOCKED`, starts workflow `claim-<id>`, and marks the row published.
- [ ] Store the trace context on the outbox row, so the trace continues across the hop.
- [ ] A placeholder `ClaimWorkflow` runs one no-op activity, then waits for the `submitted` signal. The schema keeps a claim in `draft` until it has a submission time.

Done when a new claim appears as a running workflow in the Temporal UI. If this slips, it is the first job of week 2.

## Session 6: tracing and CI

- [ ] OTEL SDK in the API, relay and worker, exporting to `otel-lgtm`; JSON logs from `slog` carry the trace ID.
- [x] GitHub Actions: `go vet`, golangci-lint, `go test -race`, migrations and `db-check` against a Postgres 18 service, an OpenAPI lint, ShellCheck, Hadolint, actionlint, a Windows job, and image builds.

Done when CI is green on main, and one create-claim trace shows in Grafana from the HTTP request to the workflow start.

## Session 7: money core, ADRs and the gate

- [ ] `internal/rules/money`: `int64` paise, basis points, half-up rounding, largest-remainder allocation.
- [ ] Table tests, plus a Go fuzz test proving allocations always add up to the total.
- [x] ADRs 0001 to 0006: Go, Temporal, the outbox for every workflow message, only IDs through Temporal, the engine as a pure library, synthetic data only.
- [ ] Gate: from a fresh clone, `go run ./cmd/dev up`, `go run ./cmd/dev migrate` and `go run ./cmd/dev test` all pass. Record a one-minute demo.

## Done by the end of week 1

- `go run ./cmd/dev up` starts Postgres, SeaweedFS, Temporal and the Grafana stack, and `go run ./cmd/dev app` adds the API, the relay and the worker.
- Creating a claim writes it with its outbox row, starts its workflow, and leaves one trace.
- Migrations, green CI, OpenAPI v0, six ADRs and a tested money package are merged.

Later weeks: the Arogya Sanjeevani rule pack, extraction, the agent and the review UI. The build plan is in [architecture.md](architecture.md#repo-structure-and-build-plan).

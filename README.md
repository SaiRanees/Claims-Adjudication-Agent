# claims-agent

An AI agent that adjudicates Indian health insurance reimbursement claims against the policy wording. The agent gathers and checks the facts from the claim documents; a deterministic rules engine decides every amount; a person reviews anything the agent isn't sure of.

The first product is Arogya Sanjeevani, on the Go Digit wording (UIN GODHLIP24179V022324).

> All data in this repository is synthetic. It contains no real claims, patients or policyholders, and no material from any employer or client. See [ADR 0006](docs/adr/0006-synthetic-data-only.md).

## Status

Week 1, the walking skeleton. In place: the local stack, the first migration with its database checks, the API contract, CI, the first ADRs, and the API's first endpoints: `GET /healthz`, `POST /v1/claims` (idempotent, writing its workflow's outbox message in the same transaction) and `GET /v1/claims/{claimId}`. The other operations answer 501 for now, and the `relay` and `worker` commands are stubs until session 5.

## Prerequisites

Development runs natively on Windows (PowerShell, cmd or Git Bash), macOS and Linux. You need:

- Docker Desktop, or Docker Engine with Compose v2
- Go 1.27
- Git

Nothing else. Project tasks run through a small Go program, `cmd/dev`, and the lint tools run from pinned Docker images ([ADR 0007](docs/adr/0007-go-task-runner.md)). On Windows, Docker Desktop runs its Linux engine in WSL 2 behind the scenes, but you never install or use a Linux distribution yourself.

## First run

```powershell
go run ./cmd/dev up          # start the stack; creates .env from .env.example the first time
go run ./cmd/dev migrate     # apply db/migrations as claims_owner
go run ./cmd/dev seed        # load one synthetic policy and member
go run ./cmd/dev db-check    # check grants, the audit hash chain and the constraints

go run ./cmd/dev tools       # once: add oapi-codegen to go.mod as a tool
go run ./cmd/dev gen         # generate internal/api/apigen from api/openapi.yaml
go mod tidy
go run ./cmd/dev check       # vet, lint and tests
```

Commit the generated code and `go.mod`/`go.sum`: CI fails if `go generate` or `go mod tidy` would change them.

`go run ./cmd/dev help` lists every task, and `go run ./cmd/dev reset` deletes all local data. On Windows, the tests run without the race detector unless a C compiler such as MinGW-w64 is installed; CI always runs them with it.

The tests that need the database skip themselves unless `TEST_DATABASE_URL` points at the migrated, seeded `claims` database as `claims_app`. With the stack up and the values from `.env.example`, in PowerShell:

```powershell
$env:TEST_DATABASE_URL = "postgres://claims_app:app-dev-only@localhost:5432/claims?sslmode=disable"
go run ./cmd/dev test
```

They leave their test claims behind, because `claims_app` can't delete claims; `go run ./cmd/dev reset` clears them.

### Calling the API

Every operation except `/healthz` needs a bearer token ([ADR 0008](docs/adr/0008-hs256-bearer-tokens.md)). `go run ./cmd/dev token` prints a 12-hour token signed with the secret in `.env`; give it a role and a subject for someone else, such as `go run ./cmd/dev token reviewer rev-1`.

```powershell
go run ./cmd/dev app
$token = go run ./cmd/dev token
$claim = @{
  memberId = "01930000-0000-7000-8000-000000000201"; admittedOn = "2026-10-01"; dischargedOn = "2026-10-05"
  hospitalName = "Synthetic City Hospital"; claimedAmountPaise = 12500000
} | ConvertTo-Json
Invoke-RestMethod -Method Post http://localhost:8080/v1/claims -ContentType application/json -Body $claim `
  -Headers @{ Authorization = "Bearer $token"; "Idempotency-Key" = "first-claim" }
```

Sending the same request again with the same key returns the same claim instead of creating a second one.

| What | Where |
| --- | --- |
| Postgres | `localhost:5432`, database `claims` |
| Temporal (gRPC) | `localhost:7233`, namespace `claims` |
| Temporal web UI | http://localhost:8233 |
| SeaweedFS S3 API | http://localhost:8333 |
| Grafana: traces, logs, metrics | http://localhost:3000 |
| OTLP (gRPC / HTTP) | `localhost:4317` / `localhost:4318` |
| API (`go run ./cmd/dev app`) | http://localhost:8080 |

Every port binds to 127.0.0.1 only.

## Database roles

| Role | Used by | Allowed to |
| --- | --- | --- |
| `claims_owner` | migrations and the seed | own and change the schema |
| `claims_app` | `api`, `relay`, `worker` | only what migrations grant, down to the column; `audit_events` is insert and read only |

`scripts/db-check.sh` compares `claims_app`'s privileges on every table with the list in the script, so a new table or grant fails the check until the list is updated on purpose. It also proves the audit chain rejects gaps, forks and wrong links, and that no role, the owner included, can update or delete audit events. The script runs inside the Postgres container, so it needs no shell on your machine.

## What CI checks

- Go on Linux: generated code is current, `go.mod` is tidy, `go vet`, golangci-lint, `go test -race`
- Go on Windows: `go vet`, build, tests, and the dev tool
- Database: roles, migrations applied twice (the second run is a no-op), the seed loaded twice, `db-check`, then the Go tests that need the database, with `-race`
- The OpenAPI contract, with Redocly
- The database check script (ShellCheck), the Dockerfile (Hadolint), the workflow (actionlint) and the Compose file
- An image build for each service

## Layout

```text
cmd/            api, relay, worker, and dev (the task runner); later evalrunner, datagen, mcpserver
internal/
  api/          HTTP handlers; apigen/ is generated from api/openapi.yaml
  rules/        the engine: pure, no I/O; money/ does paise arithmetic
  workflow/     Temporal workflows and activities
  claims/  documents/  audit/  policy/  review/
  agent/  llm/  extraction/
  platform/     config, database, OpenTelemetry, Temporal client, auth
api/            the OpenAPI contract
db/             init (roles, in SQL), migrations (forward-only), seed (synthetic)
docs/plan/      the design (architecture.md) and this week's checklist
docs/adr/       architecture decision records
scripts/        db-check.sh, run inside the Postgres container
CLAUDE.md       project instructions that Claude Code reads at the start of every session
```

## Decisions

| ADR | Decision |
| --- | --- |
| [0001](docs/adr/0001-use-go.md) | Use Go for the services and tools |
| [0002](docs/adr/0002-temporal-for-claim-workflows.md) | Run each claim as a Temporal workflow |
| [0003](docs/adr/0003-outbox-for-every-workflow-message.md) | Send every workflow message through a transactional outbox |
| [0004](docs/adr/0004-only-ids-through-temporal.md) | Pass only IDs through Temporal |
| [0005](docs/adr/0005-pure-rules-engine-and-paise.md) | Make the rules engine a pure library, with money in paise |
| [0006](docs/adr/0006-synthetic-data-only.md) | Use synthetic data only |
| [0007](docs/adr/0007-go-task-runner.md) | Run development tasks with a Go program, not Make |
| [0008](docs/adr/0008-hs256-bearer-tokens.md) | Authenticate API callers with HS256 bearer tokens |

New decisions start from [the template](docs/adr/0000-template.md).

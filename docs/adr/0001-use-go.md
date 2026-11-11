# 0001. Use Go for the services and tools

- Status: Accepted
- Date: 2026-11-11

## Context

One developer is building an API, an outbox relay, Temporal workers and several command-line tools over about eight part-time weeks. The code has to be strongly typed (it computes money), quick to build and test, easy to ship as containers, and well supported by the two platforms at the centre of the design: Temporal and the Anthropic API.

## Decision

Write every backend service and tool in Go 1.27, in one module (`github.com/SaiRanees/Claims-Adjudication-Agent`). The web app, from week 6, uses TypeScript with a client generated from the same OpenAPI contract.

## Consequences

- Each service is one static binary on a distroless image, running as a non-root user.
- Builds and tests are fast, and `go test -race` catches data races in the relay and workers.
- Temporal's Go SDK, the official Anthropic Go SDK (v2, which needs Go 1.25 or later) and pgx are first-class libraries.
- There is no framework doing wiring behind the scenes: configuration, dependency wiring and validation are explicit code. That is more to write, and easier to read and explain.
- Error handling is verbose, and the lint rules in `.golangci.yml` keep it consistent.

## Alternatives considered

- **Java 21 with Spring Boot.** A mature ecosystem and a good Temporal SDK, but a heavier runtime, slower feedback, and more framework behaviour to understand before changing it.
- **Python.** The richest AI tooling, but weaker typing for a money-critical engine and harder to ship as small, self-contained images.
- **TypeScript on Node.js.** One language with the web app, but no integer type that holds every int64 amount exactly.

# 0002. Run each claim as a Temporal workflow

- Status: Accepted
- Date: 2026-11-11

## Context

A claim lives for hours to weeks. Extraction and agent steps call models that time out, rate-limit and fail. The claim may wait for missing documents, then for a reviewer who holds a time-limited lease. The policy wording sets deadlines for settling or rejecting a claim, so timers matter. Built by hand, this is a state machine, a job queue, retry logic and scheduled jobs, each with its own failure cases.

## Decision

Each claim is one Temporal workflow with the ID `claim-<claim id>`. It is started and signalled only through the outbox ([0003](0003-outbox-for-every-workflow-message.md)). Activities do all the I/O. Postgres stays the system of record; Temporal holds only in-flight orchestration state, and only IDs ([0004](0004-only-ids-through-temporal.md)).

## Consequences

- Retries with backoff, durable timers, signals and a full event history come with the platform, and a failed run can be replayed to see exactly what happened.
- The workflow ID makes duplicate starts harmless: a second start for the same claim finds the run that already exists.
- Workflow code must be deterministic: no clock, randomness, network or goroutines except through the SDK. Changing a workflow that has runs in flight needs versioning.
- We run a Temporal service: the CLI's dev server locally, and a self-hosted cluster or Temporal Cloud later.
- History size and payload limits apply, which [0004](0004-only-ids-through-temporal.md) addresses.

## Alternatives considered

- **A Postgres state machine with a job queue.** Fewer moving parts, but timers, retries, long waits and their tests would all be ours to build.
- **AWS Step Functions.** Managed, but it ties the project to one cloud and describes workflows in JSON rather than Go.
- **Event choreography over Kafka.** More infrastructure, and the flow of a claim becomes hard to see in one place.

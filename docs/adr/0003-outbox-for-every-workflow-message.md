# 0003. Send every workflow message through a transactional outbox

- Status: Accepted
- Date: 2026-11-11

## Context

When the API changes a claim in Postgres, the claim's workflow has to hear about it. Writing to Postgres and calling Temporal directly is a dual write: a crash between the two either loses the message or starts a workflow for a change that was rolled back.

## Decision

Every message to a workflow, whether a start, documents added, a submission or a review decision, is a row in `outbox`, written in the same transaction as the change that causes it. A relay process delivers the rows:

- It takes unpublished rows in `id` order with `SELECT ... FOR UPDATE SKIP LOCKED`, so several relays can run without delivering a row twice at once.
- Starts use the workflow ID `claim-<id>`, so a repeated start finds the existing run.
- Signals carry the row's `event_id`, and the workflow ignores IDs it has already seen.
- Each row stores the W3C trace context of the request that wrote it, so one trace runs from the HTTP request to the workflow.
- On failure the relay records the error, increments `attempts` and sets `next_attempt_at` with backoff.

## Consequences

- No lost messages and no phantom workflows. Delivery is at least once, so every receiver is idempotent.
- Pending work is visible in one table. Outbox lag (the age of the oldest unpublished row) is a metric with an alert.
- Delivery waits for the next relay poll. A short poll interval, or LISTEN/NOTIFY later, keeps that under a second.
- A claim's messages must reach its workflow in order, so the relay keeps a claim's rows in sequence even when it delivers different claims in parallel.
- Published rows need a retention job.

## Alternatives considered

- **Call Temporal, then commit.** A crash after the call starts a workflow for a claim that doesn't exist.
- **Commit, then call Temporal.** A crash after the commit loses the message, and nothing notices.
- **Change data capture (Debezium).** The same guarantee with much more infrastructure.

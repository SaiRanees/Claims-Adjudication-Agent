# 0004. Pass only IDs through Temporal

- Status: Accepted
- Date: 2026-11-11

## Context

Temporal stores every workflow input, activity input and activity result in the workflow's event history, which is replicated and kept for the namespace's retention period. A single payload is limited to 2 MB by default, and a workflow's history is limited to 50 MB or 51,200 events. Claim documents and extracted text are large, and they are personal health data that must be deletable.

## Decision

Workflow and activity inputs and results carry only IDs, versions and small enumerations: a claim ID, document IDs, an adjudication version, a model-call ID. They never carry documents, extracted text, prompts or model responses. Activities read and write Postgres and the object store themselves.

The database backs this up: an outbox payload over 2 KB fails a CHECK constraint.

## Consequences

- Histories stay small and quick to replay, and the payload limits never come into play.
- Personal data stays in Postgres and the object store, where it can be found and deleted, not in workflow histories.
- Activities do more reads.
- An activity's inputs must be pinned by version, such as the adjudication version or rule-pack version, so a retry or replay sees the same data it saw the first time.
- Debugging means following IDs from the workflow history into Postgres.

## Alternatives considered

- **Pass the data and encrypt it with a payload codec.** The data still lives in histories for the retention period, and the size limits still apply.

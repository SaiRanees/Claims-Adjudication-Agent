# 0014. Record and replay model calls, with committed cassettes

- Status: Accepted
- Date: 2026-11-24

## Context

From M2 on, code calls models: Claude Haiku 5.5 classifies pages and Claude Sonnet 5.5 extracts line items. A model's answer to the same request can differ from run to run, and Haiku 5.5 and Sonnet 5.5 reject non-default sampling settings, so there is no setting that makes an answer repeat. Yet:

- Unit tests and the extraction eval must give the same result on every run, on Windows and in CI, or a regression can't be told from noise.
- CI has no API key and shouldn't spend money or depend on the provider being up.
- Tuning a prompt means comparing runs that differ only in that prompt.

architecture.md already calls for a record-and-replay mode keyed by request hash. This ADR fixes what the hash covers, where recordings live and who may change them.

One constraint comes from [ADR 0013](0013-pdfium-reads-pdfcpu-writes-gopdf-draws.md): pdfcpu stamps every file it writes with the current time, so the bytes of a split page or page range differ on every run.

## Decision

**Three modes,** set in config:

- `live` calls the provider and writes nothing.
- `record` replays any request that has a cassette, calls the provider only for those that don't, and writes a cassette for each new answer. `--rerecord <job>` replaces one job's cassettes deliberately.
- `replay` reads cassettes only. A miss fails the run and names the job and the request hash.

The worker accepts only `live` and refuses to start in any other mode. `record` and `replay` belong to tests and `cmd/evalrunner`, on generated packets only ([ADR 0006](0006-synthetic-data-only.md)).

**The request hash** is a SHA-256 over a canonical encoding with a fixed field order and no maps, so the same request hashes the same on every OS. It covers:

- the provider and the model ID;
- the job, and the prompt's ID, version and SHA-256;
- the output schema's SHA-256;
- the parameters: token cap, thinking setting and effort;
- each content part, in order: a text part by the SHA-256 of its bytes; an uploaded file by its SHA-256; a derived file (a page, a page range, a photo's PDF) by the uploaded file's SHA-256, its page numbers and a derivation version that is bumped by hand when the way it is derived changes;
- a sample index, 0 unless a step asks the same question more than once, as week 4's two-pass agreement on scans does. It is never sent to the model.

A re-ask carries the earlier answer and the validation error as text parts, so it hashes differently from the first ask. The hash never covers the API key, the SDK version, times or trace IDs. A test pins one request's hash on the Linux and Windows CI jobs.

**A cassette** is one JSON file per request, `evals/cassettes/<job>/<hash>.json`, committed with LF endings. It holds the request's summary (everything the hash covers, as identities, never document bytes), the output exactly as returned, the stop reason, the token counts, the model that answered, the cost in US dollar micros and when it was recorded. A refusal or a `max_tokens` stop is recorded like any other answer, so replay fails the same way the recording did.

**Cost:** before a `record` or `live` run, `cmd/evalrunner` estimates the run's cost from page counts and recent tokens per page, and refuses to start above `--max-usd` ($10 by default). During the run it stops when the money actually spent reaches the cap.

**CI** replays the extraction eval on every pull request, with no key, and compares the replayed report's metrics with the latest committed report. A miss or a difference fails the build. Replay is deterministic, so a difference means code changed a number, and the fix is a new report from a replay, committed with the change.

**Nobody edits a cassette by hand** (CLAUDE.md, rule 9). A cassette changes only through a `record` run. The eval lists cassettes no request used, and `--prune` deletes them.

## Consequences

- Tests and the eval give the same numbers on every machine, and CI needs no key.
- Anything that changes a request invalidates its cassettes: a prompt, a schema, a model, a thinking setting, a derivation version, or a byte of a generated packet. That costs a re-record, about $5 to $10 for the dev half, which `--max-usd` bounds. Iterating on a small slice of packets first keeps prompt work cheap.
- A cassette proves what a model said once, not what it says today. Drift in the model shows up only when cassettes are re-recorded; a model upgrade needs a fresh recording and a passing eval, as architecture.md already requires.
- A derived file's bytes aren't part of the hash, so a pdfcpu change that alters a page's content wouldn't miss a cassette by itself. The pinned, ID-stripped derived-file test in ADR 0013 catches that, and the fix is to bump the derivation version and re-record.
- The repository grows by a few megabytes per recording of the dev half.

## Alternatives considered

- **Hand-written mock responses.** They test the plumbing, but they aren't evidence of what a model does, and the eval would grade answers someone wrote.
- **Calling the model in CI.** It needs a key in CI, costs money on every pull request, and fails when the provider does.
- **Hashing the exact bytes sent.** pdfcpu's output carries the current time, so no recording would ever match.
- **Keeping cassettes outside git,** in Git LFS or the object store. That adds setup on Windows and in CI for a few megabytes that fit in git, and loses review in the pull request.
- **Reusing `model_calls`.** That table keeps production results by claim, step and request hash so a retried activity doesn't pay twice. It lives in one database, while cassettes travel with the code to every machine.

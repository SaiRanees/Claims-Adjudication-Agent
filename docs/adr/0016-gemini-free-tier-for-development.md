# 0016. Use Gemini's free tier as the development model provider

- Status: Accepted
- Date: 2026-11-27

## Context

Week 3's plan, as accepted on 24 November, put every model call on Claude: Haiku 5.5 to classify pages and Sonnet 5.5 to extract line items, with a $20 to $30 Console spend limit. Sai has decided not to spend money on this project, so a paid API isn't available for development. Google AI Studio gives an API key on a project with no billing, with a free tier that covers models able to read PDFs and answer to a JSON schema.

architecture.md already keeps the provider behind one interface in `internal/llm`, with prompts versioned per provider and evals run per provider and model, so changing the first provider is a configuration and adapter decision, not a redesign.

## Decision

**Gemini's free tier is the development provider.** The first adapter in `internal/llm` uses Google's official Go SDK, `google.golang.org/genai`, pinned at v1.79.1, against the Gemini Developer API with `GEMINI_API_KEY`. The Claude adapter waits until there is a budget; the `Provider` interface, the request hash and the cassette format stay provider-neutral so it can be added without changing them.

**Models,** from the free tier's list on AI Studio's rate-limit page, read by Sai on 27 November 2026:

| Job | Model | Free tier: requests a minute | Input tokens a minute | Requests a day | Thinking |
| --- | --- | --- | --- | --- | --- |
| Page classification | `gemini-3.5-flash-lite` (stable) | 15 | 250,000 | 500 | `minimal`, the lowest level; Google calls it near-off |
| Line-item extraction | `gemini-4-hydrogen` | 5 | 250,000 | 20 | `low`, the lowest level; Gemini 4 models can't turn thinking off |

`gemini-4-hydrogen` is the only Gemini 4 model with a free tier (Neon, Carbon and Argon have none), so it is the strongest free choice for line items. Free limits apply per project, not per key, and the daily count resets at midnight Pacific time.

**Requests:** PDFs go inline as `application/pdf` parts. Output is JSON with a response schema. Thinking is set per job to the lowest level the model allows, and the level is part of the request hash. The SDK's own retries are off.

**Rate limits are respected on our side.** A client-side limiter holds each model under its requests a minute, input tokens a minute and requests a day, with the figures above in config. The day's count persists in a git-ignored file keyed by the Pacific date, so a second run on the same day doesn't start from zero. A 429 from Google stops the run cleanly rather than retrying; every cassette already written stays.

**Record is resumable.** As ADR 0014 already says, `record` calls only for requests that have no cassette. A run cut off by the daily limit is simply run again the next day and continues where it stopped.

**Cost** is still computed from a per-model price table in US dollar micros. Free-tier models record $0 for every figure, and the table keeps its structure (input, output, cache and batch prices, and any price tier by prompt size), so a paid provider or Gemini's paid tier can be added by filling in rows.

**ADR 0014's record and replay is unchanged.** The provider and model ID were always part of the request hash, so Gemini cassettes and any later Claude cassettes never collide. CI replays with no key.

**Schema limits.** Google publishes no numeric limits for response schemas. It says optional properties, enum values and `anyOf` branches add to a schema's complexity, and that a schema too large or too deeply nested is rejected with `400 INVALID_ARGUMENT`. So the schema test enforces our own bounds: no optional properties (ADR 0015 already requires every property), and limits on nullable properties, enum values and nesting depth, kept close to what the line-items schema needs. The live smoke test sends each job's real schema, so Google's acceptance is proven on the developer's machine.

## Consequences

- No money is spent. The key is on a project with no billing, so a mistake can't run up a bill.
- **Rate limits set the pace.** Classifying the dev half's ~350 pages fits in one day (500 requests a day). Extraction at 20 requests a day takes several days for the dev half, more with re-asks, so sessions 7 and 8 plan recordings across days, starting with a five-packet slice.
- **Google may use free-tier prompts and responses to improve its products,** and human reviewers may read them. That is acceptable only because every document is synthetic ([ADR 0006](0006-synthetic-data-only.md)). It rules out real data on this tier forever: before any real document is used, the project needs a paid provider whose terms don't allow training on it, as architecture.md's privacy section already requires.
- **Accuracy is to be measured, not assumed.** The plan's figures were for Claude. Session 8's first scored run is the first evidence for Gemini, and a switch of model or provider later needs a passing eval, as architecture.md requires.
- `gemini-4-hydrogen` carries no "stable" badge, and during the first probe it answered `503` ("high demand") once. A 503 is a failed attempt; record resumes past it on a later run.
- Thinking can't be turned fully off on either model, and thinking tokens count against the output cap. Token caps leave room for them, and a `MAX_TOKENS` stop is a failed attempt, as before.
- Free-tier limits and model lists change without notice. They live in config, and the ADR records the figures read on 27 November.
- Prompts live under `prompts/<job>/gemini/`, per architecture.md's per-provider prompts.

## Alternatives considered

- **Claude with a spend limit,** as planned. Ruled out by the no-spending decision; its adapter comes back when there is a budget.
- **Gemini's paid tier.** It isn't free, and a free project with no billing can't be charged by mistake.
- **A local open model** (Gemma through Ollama). It needs a GPU or a slow CPU run, can't read PDFs natively, and Gemma's hosted free tier allows only 16,000 input tokens a minute, a few pages.
- **A community Gemini SDK, or the raw REST API.** The official SDK is maintained by Google, and the plan allows one new dependency for the adapter.
- **`gemini-3.8-flash` for line items.** It is stable but has the same 5 a minute and 20 a day as `gemini-4-hydrogen`, from an older generation. If hydrogen's accuracy or availability disappoints, it is the first alternative to evaluate.

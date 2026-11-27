# Week 3 · Intake and extraction, first half (M2)

By the end of week 3, a synthetic packet uploaded through the API is checked file by file and submitted, with every step in the claim's audit chain. A generator renders 100 packets and their ground truth. Page classification and line-item extraction run on the dev half through one model layer that records and replays, and a first run is scored against the truth. Week 4 finishes M2: discharge-summary facts, degraded scans, the workflow's preparation and extraction steps, and the baseline that is M2's gate.

Progress, 24 November: the draft is reviewed against the repo and against the libraries' own source, and this plan folds in the changes (see "Review notes" at the end). The plan is accepted as reviewed: every "Decide first" item is ticked, ADRs 0013 to 0015 are accepted, and the new dependencies are approved for the sessions the table names. The API key with its spend limit is still Sai's to create before session 5.

Session 1 is done, 24 November. Every audit event from `claim.created` on is chained under the claim's lock and recomputes from the stored rows. Files register with a 15-minute `PUT` URL signed for their type and length, and complete checks size and SHA-256 outside any transaction. Against SeaweedFS 4.53, a body one byte longer or shorter than signed, a chunked body and another `Content-Type` are all refused. aws-sdk-go-v2's default checksums never reach a presigned URL, so they stay on; the client is built from the S3 settings directly, without the SDK's `config` module. CI's Database job runs the upload tests against SeaweedFS and fails rather than skips without them.

Session 2 is done, 25 November. go-pdfium v1.21.5 (WebAssembly, on wazero v1.12.0) and pdfcpu v0.16.4 passed every proof before anything relied on them: nothing on the host opens through the sandbox, a 1 GiB inflate bomb is `unreadable` in about 3 s against the 256 MiB limit, a 200 ms deadline stops a 5 s parse, and pdfcpu turns a JPEG and a PNG into PDFs. A 10-page PDF checks in 15 to 50 ms and a 12 MP photo in 13 to 50 ms, so the worker runs two PDFium workers with 30 s deadlines. pdfcpu runs with a stateless configuration, since its default creates a directory under the user's config dir. The workflow checks each uploaded file, and submit needs one accepted file and none pending; the acceptance test sees every verdict, and a hundred complete-against-submit runs never both commit. Sai terminated the dev workflows started on the placeholder on 25 November.

Session 3 is done, 25 November. gopdf v0.38.6 passed its proof first: with static Noto Sans (from pinned upstream commits, hashes in `internal/datagen/fonts/SOURCES.md`) it renders `₹1,23,456.00`, `Rs. 123,456/-` and a long description, two processes give identical bytes with no `/CreationDate`, `/ID` or `/Encrypt`, and PDFium reads every string back. gopdf always zlib-compresses the embedded font subset, whatever `SetNoCompression` says, so content streams are uncompressed but the font isn't; session 4's pinned packet hashes on both CI jobs are what catch a change in `compress/flate`. It brings two indirect modules, `phpdave11/gofpdi` and `pkg/errors`. The 52 golden specs are in `evals/extraction/scenarios`, and all 52 packets render byte-identically in two processes, pass the self-check, and give every checked row of `golden-cases.csv` when the engine runs on their `truth.json`. PDFium prints each bill row as one line of text, so the self-check requires a line's date, description, quantity, unit price and amount on one line of its page, not merely somewhere on it.

Session 4 is done, 26 November. The claim form (two pages), photo ID, discharge summary (two pages), OT notes and implant invoice print the footer on every page, and truth gains `other_documents`: every value each prints, by page, with a row's values (an invoice item, a stay segment, an implant and its serial) required on one text line, and an invoice item's quantity, rate and amount held to its facts line. The ID is a plain card from an invented "Synthetic Test Registry" that says it isn't issued by any government, with no emblem or seal, and a `SYN-ID-XXXX-9999` number that validation enforces. Fields only these documents print are optional in a spec, so the golden specs parse unchanged, and the generator's version is 2. `datagen scenarios` wrote the 48 seeded specs from a PCG seeded by each ID's SHA-256, keeping a spec only if it parses, renders and adjudicates with no flag but `bill_discount`, `icu_ratio_scaling` or `cover_exceeded`; packets run to 8 to 12 pages. The seeded packets have no checked figures, so their engine test checks that truth.json's facts are the spec's, adjudicate cleanly, and that the claim form claims the bills' net total. A golden case's claim type is the first of a cataract tie, an ICU segment, an accident or ambulance, an implant or procedure, and a long or pharmacy-heavy stay, or else medical; the split is 51 dev and 49 test, no type's halves more than one apart. The pinned packets are `golden-01-worked-example` and `seeded-surgery-implant-03`, which prints each of the eight document types in a file of its own; their hashes matched on Windows and in a Linux container before CI ran.

Changed on 27 November, before session 5: the model layer uses Gemini's free tier instead of Claude, because the project spends no money ([ADR 0016](../adr/0016-gemini-free-tier-for-development.md)). The models item, the key item, the dependency table and sessions 5 to 8 are updated to match. ADR 0014's record and replay is unchanged. The free tier's daily limits stretch the extraction recording over several days.

## Decide first

- [x] **Split M2 across two weeks.** Week 3 builds the pieces and scores a first run on the dev half. Week 4 adds discharge-summary facts, degraded scans and two-pass agreement, moves preparation, classification and extraction into the workflow, asks for missing documents, and sets the baseline that is M2's gate. Week 3 has eight sessions rather than seven, because the generator didn't fit in one sitting. If the week runs long, rate limits and the OT-notes and implant-invoice templates move to week 4 first; never a self-check, the split or the replay gate.
- [x] **A file isn't a document.** One upload can hold several documents, such as a bill and a discharge summary scanned together. This plan calls an upload a *file*, a row in `documents` (which the API calls a document), and a logical document a *part*: a type and a page range within one file. Page classification splits files into parts, and extraction works on parts.
- [x] **The model copies; code converts** ([ADR 0015](../adr/0015-model-copies-printed-values-code-converts.md)). Extraction returns amounts, unit prices, quantities and dates as the strings printed on the page. Code parses them into paise, whole quantities and dates, and checks them against the page's text. The model never converts, adds up or fills in a value. Every schema property is required, and nullable where a value can be missing, so no schema has optional parameters.
- [x] **PDF libraries** ([ADR 0013](../adr/0013-pdfium-reads-pdfcpu-writes-gopdf-draws.md)). go-pdfium in WebAssembly mode reads: it is the first code to parse an upload, decides `encrypted`, `unreadable` and the page count, reads text with positions and renders pages. pdfcpu writes: it splits, merges and turns photos into PDFs, and only ever sees files PDFium accepted. gopdf draws the generator's packets. All three are pure Go, so the worker image stays `CGO_ENABLED=0`. Four facts from their source change the draft:
  - pdfcpu's output isn't byte-stable and can't be made so: every write hashes `time.Now()` into the trailer's `/ID`, with no option to stop it. So a split page or page range is identified by the uploaded file's SHA-256 and its page numbers, and a repeated page by a hash of PDFium's rendering, never by bytes pdfcpu wrote.
  - pdfcpu opens a PDF whose user password is empty without asking for one, so `encrypted` is decided by PDFium's security handler, not by whether opening fails. pdfcpu ignores EXIF orientation.
  - go-pdfium's WebAssembly pool mounts the host's root directory into the module (on Windows, the working directory's drive) unless it's given an `FSConfig`, and sets no memory limit. The sandbox holds only with an empty `FSConfig`, a memory limit, `WithCloseOnContextDone` and a deadline on every call.
  - pdfcpu parses natively, outside that sandbox. The draft's "hostile PDFs get parsed inside a WebAssembly sandbox" was true of PDFium only.
- [x] **New dependencies,** asked for here as CLAUDE.md requires, and approved on 24 November, each for the session the table names. Each arrives in the session that first needs it, with its version pinned in go.mod.

  | Module | For | Session |
  | --- | --- | --- |
  | `github.com/aws/aws-sdk-go-v2` (config, `service/s3` and its presigner) | Presigned uploads; reading and writing objects | 1 |
  | `github.com/klippa-app/go-pdfium` (WebAssembly mode, with wazero) and `github.com/pdfcpu/pdfcpu` | File checks, then pages | 2 |
  | `github.com/signintech/gopdf` | The generator | 3 |
  | `google.golang.org/genai`, v1.79.1 | The Gemini adapter ([ADR 0016](../adr/0016-gemini-free-tier-for-development.md), approved 27 November) | 5 |

  Session 3 also commits static Noto Sans Regular and Bold TTFs with their licence, `OFL.txt` (SIL Open Font License 1.1).
- [x] **Presigned uploads enforce the size.** aws-sdk-go-v2's signer signs `Content-Type`, and signs `content-length` whenever the request has a length. SeaweedFS 4.53, the Compose image, rebuilds a signed `content-length` from the body's actual length, for presigned URLs as well as signed headers, so a body of any other size, or a chunked one, fails the signature. The gap the draft allowed for doesn't exist. Session 1 proves it against SeaweedFS.
- [x] **A deterministic generator.** The same scenario spec and generator version give byte-identical PDFs on every run and on both OSes, so the cassettes keep matching. It draws with gopdf and an embedded static Noto Sans, which has the ₹ sign (U+20B9) that PDF's built-in fonts lack: no protection, no creation date, and compression off where gopdf allows it, so a Go upgrade's changes to `compress/flate` can't move the bytes. Hospitals and people are invented, no ID copies a real one's design or number format, and every page carries a "SYNTHETIC TEST DOCUMENT" footer. Seeded scenario specs are generated once and committed, like golden cases, so a later change to the generator's code can't quietly change them.
- [x] **100 packets in v1, toward architecture.md's 200.** The 52 golden cases are rendered with their checked facts unchanged, and 48 seeded scenarios come from six claim types.
  - Golden packets are short and shaped by the engine's edge cases, so every metric is reported for golden packets, seeded packets and all of them.
  - A committed split file puts each scenario in the dev or test half for good: within each claim type, scenarios sorted by the hash of their ID alternate between halves. The file is append-only: a later scenario is appended and split the same way among the new ones, and a test pins the first 100 entries.
  - Rendering and the self-checks cover both halves. No model call and no metric touches the test half without `--release`.
  - The set grows to 200 by appending seeded scenarios, by M5.
- [x] **Two models, set in config, with thinking pinned.** Changed on 27 November ([ADR 0016](../adr/0016-gemini-free-tier-for-development.md)): the models are on Gemini's free tier, because the project spends no money. The Claude plan (Haiku 5.5 and Sonnet 5.5) waits until there is a budget.
  - `gemini-3.5-flash-lite` classifies pages: 15 requests a minute, 250,000 input tokens a minute and 500 requests a day on the free tier. `gemini-4-hydrogen` extracts line items: 5 a minute, 250,000 tokens a minute and 20 a day. Both cost $0.
  - Each job's thinking level is config, recorded with the call and covered by the request hash. Neither model can turn thinking off, so each job starts at the lowest level it allows: `minimal` for flash-lite, `low` for hydrogen. Copying needs no reasoning.
  - No fallbacks to another model. A fallback answers with a model no eval has run, which architecture.md rules out; a refusal or a block is a failed attempt.
  - One pass over the dev half, about 350 pages with about 210 of them bill pages, is about 350 classification calls (within one day's 500) and on the order of 100 extraction calls of up to four pages, before re-asks: five or more days at 20 a day. Record is resumable and a client-side limiter keeps runs under the limits, so a recording simply continues the next day.
- [x] **Record and replay** ([ADR 0014](../adr/0014-record-and-replay-model-calls.md)). Model responses for synthetic packets are committed as cassettes, keyed by request hash, and CI only replays them, so it needs no key. A missing cassette fails the run. Changed from the draft:
  - A derived file (page, page range, photo's PDF) is identified by the uploaded file's SHA-256, its page numbers and a derivation version, because pdfcpu's bytes change on every run.
  - `record` calls the API only for requests without a cassette, so a second recording doesn't pay again for the first.
  - A sample index joins the hash, and is never sent, so week 4's two passes over a scan don't replay one answer twice.
  - The worker accepts only `live`, so no cassette is ever written from real data.
  - CI replays on every pull request, not on a path filter: bumping pdfcpu or gopdf in go.mod changes packets without touching any listed path. It compares the replayed metrics with the committed report.
  - The cost cap stops a run in progress as well as refusing one whose estimate is over it.
- [x] **Sai: an API key that can't spend money.** Changed on 27 November (ADR 0016).
  - The key is from Google AI Studio, on a project with no billing, so nothing can be charged. Free-tier limits apply per project, and Google may use free-tier prompts to improve its products, which is acceptable only because every document is synthetic ([ADR 0006](../adr/0006-synthetic-data-only.md)).
  - It is in `.env`, which git ignores, as `GEMINI_API_KEY`. `go run` on the host doesn't read `.env` (Compose does), so the dev tool passes the key to record and live runs, never printing it. CI never gets it.
  - Done, 27 November.
- [x] **Extraction can't act.** Classification and extraction calls get no tools. The prompts mark pages as untrusted data, and each schema has a field for instruction-like text: at most three snippets of 200 characters. Code also scans each page's text for such phrases. Both are recorded now, and M3 routes them to review.
- [x] **Lock the claim's row first.** Every transaction that writes an audit event or an outbox row for a claim, or changes its files or status, starts with `SELECT … FOR UPDATE` on the claim's row, before it reads anything it checks.
  - It gives the audit chain its next sequence number without a race.
  - It keeps a claim's outbox rows in commit order. The outbox's identity values are handed out at insert, not at commit, so without the lock a later row could become visible first, and the relay's per-claim order ([ADR 0003](../adr/0003-outbox-for-every-workflow-message.md)) would break.
  - It closes a race the draft left open: complete reads the claim as `draft`, submit finds no file `uploaded`, and both commit, leaving an uploaded file and its signal after the submission.
  - The claim's row is always locked before any document row, so two transactions can't deadlock, and no object-store I/O happens while the lock is held.
- [x] **A check that runs out of retries rejects the file, not the claim.** The draft moved the claim to `failed`, but the claims table's CHECK constraints set `submitted_at` for every status except `draft`, so a draft claim can't be `failed`. `CheckDocument` retries infrastructure errors with backoff up to a 5-minute interval, for about an hour. Then the file is `rejected` with `check_failed`, the claim stays a draft, and an error log raises it for an operator. The submitter uploads the file again.

## How a packet moves

Week 3 runs steps 1 to 5 in the claim's workflow, and steps 6 to 10 in the eval harness on generated packets. Week 4 moves steps 6 to 9 into the workflow.

1. **Create the claim.** This already works: the claim, its outbox row and its idempotency key commit together, and the relay starts `claim-<id>`. Week 3 adds the first audit event, `claim.created`, to that transaction.
2. **Register a file.** Under the claim's lock, the API checks that the claim is `draft` or `awaiting_documents` and has fewer than 50 files, and records the declared type, size and SHA-256. It returns a `PUT` URL into `quarantine/<claim>/<file>`, signed for the declared type and length, that lasts 15 minutes.
3. **Complete the upload.** The API reads the stored object's size, then streams and hashes it, outside any transaction. Then, under the claim's lock, it re-checks the claim's state and marks the file `uploaded`, with the `documentUploaded` outbox row and an audit event. A missing object or a mismatch marks it `rejected`.
4. **Check the file.** A worker activity re-reads and re-hashes the bytes, checks the magic bytes, and opens a PDF in PDFium's sandbox, or checks a photo's header and turns it into a one-page PDF. It writes the accepted copy from the bytes it verified, then records `accepted` with a page count, or `rejected` with a reason.
5. **Submit.** This needs at least one accepted file and none still `uploaded`, so no check is pending. The status, the `submitted` outbox row and an audit event commit together.
6. **Prepare.** pdfcpu splits each accepted file into pages. PDFium reads each page's text with positions, and renders it for a fingerprint that marks repeated pages.
7. **Classify pages.** `gemini-3.5-flash-lite` types each page and copies its printed markers, such as a bill number or "page 2 of 3". Code groups the pages into parts and lists what the packet lacks.
8. **Extract line items.** `gemini-4-hydrogen` reads each bill up to four pages at a time and copies every line as printed. Code parses the values.
9. **Check.** Code checks the schema, grounding in the page text, quantity times unit price, and the bill's totals. A failure gets one re-ask; a second marks the step failed or flags the lines.
10. **Score.** The harness matches extracted lines to the generator's truth and reports the metrics.

Nothing here rejects a claim. A rejected file waits for a new upload, and anything flagged waits for M3's review routing.

## Data

As in week 2, a table arrives when something writes to it. Week 3 changes `documents` and starts the audit trail; week 4 adds the page, part and extraction tables when the workflow writes them. The eval harness writes no database rows, so `model_calls` waits for week 4's activities too.

| Change | When | Holds | Key rule |
| --- | --- | --- | --- |
| `documents`: status `accepted`; reasons `type_mismatch`, `image_too_large` and `check_failed`; `checked_at`; `pdf_sha256` | Week 3, migration 000002 | The worker's verdict on each file, and the SHA-256 of the PDF later steps read: the upload itself, or a photo's PDF | Status only moves forward, enforced by a trigger. `accepted` needs `uploaded_at`, `checked_at`, a page count and `pdf_sha256`; `rejected` needs a reason |
| `audit_events` (exists) | Week 3 | First writes: `claim.created`, `document.registered`, `document.uploaded`, `document.accepted` or `document.rejected`, and `claim.submitted` | IDs, codes, sizes and amounts only, never a file name; hash-chained per claim, in the same transaction as the change |
| Object store layout | Week 3 | `quarantine/<claim>/<file>` until checked, then `claims/<claim>/<file>/original`, with a photo's PDF beside it as `document.pdf` | Keys come from IDs, never the client's file name; later steps read only the accepted copy |
| `document_pages` | Week 4 | Per page: its fingerprint, its text with positions, which page it repeats, its type and its printed markers | Unique per file and page number |
| `document_parts` | Week 4 | Parts: a type and a page range within one file | Ranges in one file don't overlap |
| `extractions` | Week 4 | Model output per part and page range, schema version, check results, model call | Insert-only; a re-extraction adds a row |

Migration 000002 has to replace two of 000001's constraints, not only widen lists. `(status = 'uploaded') = (uploaded_at IS NOT NULL)` would stop an accepted file from keeping its upload time. The new rules: a `pending_upload` file has neither time, an `uploaded` one has `uploaded_at` only, an `accepted` one has both, and a `rejected` one has `checked_at` exactly when it has `uploaded_at`, because complete rejects before upload and the worker after. 000001's table-level CHECK constraints have generated names, so session 2 reads them from `\d documents` before writing the migration. The grant for the new columns and `scripts/db-check.sh` change in the same commit.

Page text is document content. From week 4 it lives in Postgres, and never in logs, traces, audit events or Temporal.

## Session 1: the audit writer, registering and completing uploads

- [x] `internal/audit` appends events in the caller's transaction, after the caller has locked the claim's row. Each event takes the claim's next sequence number and the previous event's hash.
  - The hash is SHA-256 over the previous hash and a canonical encoding of the event: claim ID, sequence, time, actor, action and details, with keys sorted. The time is set in Go and truncated to microseconds, as Postgres stores it, and `details` holds only strings and integers. `jsonb` re-orders keys when it stores them, so the hash is never computed from text read back out of `jsonb`.
  - Events hold IDs, codes, sizes and amounts, never names, file names or document text.
  - Creating a claim writes `claim.created` in its transaction. The chain verifier comes later, but a test now reads a claim's events back from Postgres and recomputes every hash.
- [x] The S3 client talks to SeaweedFS on `S3_ENDPOINT` with path-style addressing, and presigns for `S3_PUBLIC_ENDPOINT`. Check that the SDK's default request checksums don't end up in a presigned URL, where SeaweedFS and a browser would trip over them; if they do, set them to "when required".
- [x] `POST /v1/claims/{id}/documents` registers a file and returns a presigned `PUT` URL that lasts 15 minutes, signed for the declared `Content-Type` and length. The response's `headers` carry `Content-Type`; the client's HTTP library sends the length from the body.
  - Only the claim's submitter can call it, while the claim is `draft` or `awaiting_documents` and has fewer than 50 files, checked under the claim's lock. The cap bounds the workflow's history as well as storage.
  - Registering the same SHA-256 again while that file is `pending_upload`, with the same size and type, returns it with a fresh URL and a `200`, so a lost response doesn't strand the client. The contract gains that `200`. The same SHA-256 with a different size or type, or a file already past `pending_upload`, is a `409`.
  - Object keys use IDs only, never the file name the client sent.
- [x] `POST …/complete` reads the object's size, then streams it and computes its SHA-256, outside any transaction. Then, under the claim's lock, it re-checks the claim's state. A match marks the file `uploaded`, with the `documentUploaded` outbox row and an audit event; a missing object or a mismatch marks it `rejected` (`missing_object`, `size_mismatch` or `checksum_mismatch`). Calling it twice returns the same result, and once the claim is submitted it's a `409`.
- [x] Registering is rate-limited per user: a small token bucket in the API, not a new dependency, answering `429` with `Retry-After`. One bucket per API instance is enough for the demo. Session 2 puts submit behind it too.
- [x] CI's Database job starts SeaweedFS (the Compose image) and sets the S3 test variables, and `dev test` fills them from `.env`, as it already does for Postgres and Temporal. Without that, the S3 tests would skip in CI and the session would look done.

Done when the API tests run against SeaweedFS in CI's Database job, without skipping, and cover each status, each conflict and the 50-file cap; a `PUT` with a body one byte longer than signed fails at SeaweedFS; every audit hash recomputes from the stored rows; and `go run ./cmd/dev check` passes.

## Session 2: file checks, submit and rate limits

- [x] Prove the PDF libraries before relying on them, as session 3 does for gopdf. go-pdfium's pool opens a PDF from bytes with an empty `FSConfig`; a crafted PDF whose stream inflates past the memory limit fails as `unreadable` instead of growing the worker; a deadline stops a long parse; and pdfcpu turns a JPEG and a PNG into PDFs. Measure a check on a 10-page PDF and on a photo, and set the pool's size and the deadlines from that. If either library fails, choose again now, while nothing depends on it.
- [x] Migration 000002, the contract and the generated code: the `accepted` status; the `type_mismatch`, `image_too_large` and `check_failed` reasons; `checked_at` and `pdf_sha256`; the replaced constraints and the forward-only trigger (see Data); and a `429` on submit. The grants and `scripts/db-check.sh` change in the same commit.
- [x] The workflow takes `documentUploaded` signals while it waits for `submitted`, ignores an event ID it has already seen, and runs a `CheckDocument` activity for each file. It waits for every check it started before it goes past `submitted`. The placeholder's `AcknowledgeClaim` goes.
- [x] `CheckDocument` is idempotent: a file already decided returns its stored verdict. Otherwise:
  - It re-reads the bytes and re-hashes them, because the upload URL stays valid after complete and the object could have been replaced. A mismatch is `checksum_mismatch`.
  - The magic bytes must match the declared type, or it's `type_mismatch`.
  - A PDF goes to PDFium first. Any security handler is `encrypted`, whether or not the user password is empty; a file that won't load is `unreadable`; more than 500 pages is `too_many_pages`.
  - An image's header is read before anything decodes it, and over 50 megapixels is `image_too_large`, so a decompression bomb is never decoded. A photo becomes a one-page PDF through pdfcpu. Our own code reads its EXIF orientation and applies 3, 6 or 8 as the page's `/Rotate`; a mirrored orientation, which cameras don't write, is `unreadable`.
  - pdfcpu runs only after those checks pass, under a deadline, with a panic recovered as `unreadable`.
  - It writes the accepted copy under `claims/<claim>/<file>/` from the bytes it verified, never by copying the quarantine key, then commits the verdict and an audit event under the claim's lock. A retry after a crash rewrites the same keys.
- [x] A bad file is a verdict, not an error. Only infrastructure errors retry, with backoff up to a 5-minute interval, for about an hour; after that the file is `rejected` with `check_failed` (see Decide first).
- [x] `POST …/submit`, under the claim's lock, needs at least one `accepted` file and none `uploaded`. Files still `pending_upload` stay out of the packet. The status, the `submitted` outbox row and an audit event commit together, and submitting again returns the current state. It shares the per-user rate limit.
- [x] Workflows started on the old placeholder are terminated in dev. The relay starts workflows with `REJECT_DUPLICATE`, so their draft claims can't get a new one; `go run ./cmd/dev reset` clears them. Workflow versioning waits for M3, when there are histories worth keeping. Sai terminated the 235 running locally on 25 November.

Done when an acceptance test against Postgres, SeaweedFS and Temporal, run in CI's Database job:

- uploads a PDF, a photo with EXIF orientation 6, a PDF with a user password, one with an empty user password, a truncated PDF and a PNG whose header claims more than 50 megapixels;
- sees them `accepted`, `accepted` (rendered upright), `rejected: encrypted` twice, `rejected: unreadable` and `rejected: image_too_large`;
- sees a file replaced after complete come out `rejected: checksum_mismatch`;
- runs complete and submit against each other a hundred times and never sees both commit;
- and submits the claim.

And `go run ./cmd/dev check` passes.

## Session 3: the generator, part 1: specs, bills and truth

- [x] Prove the library before any template. gopdf with the embedded Noto Sans renders `₹1,23,456.00`, `Rs. 123,456/-` and a long description; rendering twice, in two separate processes, gives identical bytes; and PDFium reads every one of those strings back from the page. If gopdf fails any of these, choose again now, while nothing depends on it.
- [x] Static Noto Sans Regular and Bold TTFs, not the variable font, with `OFL.txt`, in the generator's package, embedded with `go:embed` and marked `binary` in `.gitattributes`.
- [x] A scenario spec in YAML, decoded strictly like the rule pack, holds everything a packet shows.
  - A `facts` block in the golden-case format's shape, which week 2 made for this, and a `cover` block. The generator never changes either.
  - The insured, the policy and the hospital, all invented; the condition; and the claim form's values.
  - For each line, beside its facts: the description as printed, which bill it's on, and any treatment tie. A line matched to an Annexure A list uses an item name copied from that list in the wording, taken from a short table in the generator's data that cites the list and page. The full transcription stays in M3, and the published rule pack doesn't change. Golden cases 21 and 31 match without the item's name (21 through the item's explanation, 31 through the stay's purpose), so, as Sai decided on 25 November, a line may print its own description only with an explicit `matched_by: explanation` or `matched_by: stay`, which truth records with the item.
  - Which documents to include, the bill layout, and how many lines fit on a page.
- [x] `cmd/datagen render` writes each packet's PDFs and a `truth.json` that carries the generator's version:
  - per file: each page's type, and the parts with their page ranges;
  - per bill: each line's page, its printed strings and parsed values, and the totals;
  - the documents included, so the packet checklist can be scored;
  - the claim-level facts for week 4's discharge-summary job, and the engine's facts and cover for M3.
- [x] This session's documents: three hospital-bill layouts, a pharmacy bill and a payment receipt.
  - The layouts differ in column order, date style (`22/09/2025` or `22-Sep-2025`), digit grouping (`1,23,456.00` or `123,456.00`) and currency mark (₹, `Rs.` or none).
  - Long bills run over pages with repeated headers and "Page x of y", and the totals sit on the last page. A row never splits across pages.
  - Quantities are whole numbers, as the engine requires.
- [x] The generator checks itself. A test reads every rendered page's text with PDFium, and every printed string in `truth.json` (amounts, unit prices, quantities, dates, descriptions and totals) must appear on the page the truth names. Checking amounts alone would miss a wrong date or description.
- [x] The 52 golden cases render as packets, each with its case file's `facts` and `cover` unchanged. A test runs the engine on every golden packet's truth and must get the case's checked payable from `golden-cases.csv`, so the generator can't drift from the checked figures.

Done when the 52 golden packets render byte-identically in two processes, the self-check and the engine test pass, and `go run ./cmd/dev check` passes.

## Session 4: the generator, part 2: the other documents, seeded scenarios and the split

- [x] The other documents: a claim form, a photo ID card, a discharge summary, OT notes and an implant invoice, so a surgery packet can be complete. Hospitals and people are invented, no ID copies a real one's design or number format, and every page carries the "SYNTHETIC TEST DOCUMENT" footer.
- [x] `cmd/datagen scenarios` writes the 48 seeded scenario specs once, eight for each claim type: a medical admission, surgery with an implant, cataract day care, an ICU stay with a step-down, an accident brought in by ambulance, and a long pharmacy-heavy stay. The specs are committed and reviewed like golden cases, so a later change to the scenario code can't move them. Packets run to about ten pages.
- [x] The split file, `evals/extraction/split.csv`. Each golden case gets the claim type it is closest to, and within each type, scenarios sorted by the hash of their ID alternate between dev and test. The file is append-only. A test checks that every scenario appears exactly once and each type's halves differ by at most one, and pins the SHA-256 of the first 100 entries.
- [x] Rendered packets aren't committed; they're regenerated. A test on the Linux and Windows CI jobs pins the SHA-256 of one golden and one seeded packet's files, so a platform or library difference shows up at once.

Done when `datagen render` builds all 100 packets, the self-check and the engine test pass on every one, the pinned SHA-256s match on both CI jobs, and `go run ./cmd/dev check` passes.

## Session 5: the model layer, with record and replay

- [ ] `internal/llm` has one `Provider` interface. Its request holds only what any provider can be asked for: the job, the model, a versioned prompt, the content parts (text, PDF or image, each with its identity from ADR 0014), a JSON schema, a token cap, a thinking setting and effort, and a sample index. Each adapter declares what it supports, as architecture.md says.
- [ ] The Gemini adapter ([ADR 0016](../adr/0016-gemini-free-tier-for-development.md)) uses `google.golang.org/genai` at v1.79.1 against the Gemini Developer API, with the SDK's own retries off. It sends PDFs inline as `application/pdf` parts, asks for `application/json` with the job's response schema, and sets each job's thinking level: `minimal` for `gemini-3.5-flash-lite`, `low` for `gemini-4-hydrogen`. It sends no fallbacks. The Claude adapter waits until there is a budget.
- [ ] Every output is decoded strictly into Go types and checked again in code: unknown and missing fields fail, and enum values are compared case-insensitively. A safety block, a recitation stop or a `MAX_TOKENS` stop is a failed attempt, since the output may not match the schema then.
- [ ] The request hash, the cassettes and the three modes, as ADR 0014 sets out. The worker refuses any mode but `live`. A test pins one request's hash on the Linux and Windows CI jobs.
- [ ] Cost is computed in US dollar micros from a price table in config, with every figure written out per model rather than as a multiplier: input, output, cache writes and reads, a higher tier above a prompt size where a model has one, and batch prices. Both free-tier models record $0 for every figure; the structure stays so a paid provider can be added by filling in rows.
- [ ] A client-side limiter keeps each model under its free tier's requests a minute, input tokens a minute and requests a day, from config. The day's count persists in a git-ignored file keyed by the Pacific date, when Google's daily quota resets. A 429 stops the run cleanly, keeping every cassette written. `record` calls only for requests without a cassette, so a run cut off by the daily limit continues the next day.
- [ ] Prompts live in `prompts/<job>/gemini/v1.md`, embedded with `go:embed`, with LF endings on every OS. Their hash is part of the request hash, so editing a prompt invalidates its cassettes. Session 5 writes first versions of both jobs' prompts and schemas so the smoke test can send them; sessions 6 and 7 refine them before anything is recorded.
- [ ] A schema test holds each job's schema to bounds of our own, because Google publishes no numeric limits, only that optional properties, enum values and `anyOf` branches add complexity and an oversized schema is a `400`. Every property is required, so the optional count is zero; nullable properties, enum values and nesting depth are capped near what `line_items.v1` needs. The live smoke test proves Google accepts each real schema.
- [ ] The dev tool runs record and live runs with `GEMINI_API_KEY` from `.env`. The key comes only from the environment and is never printed, logged, committed or sent to CI. A live smoke test, one call per job, runs only with `-tags live` and a key, and never in CI.

Done when the replay tests pass with no key on Linux and Windows, the pinned request hash matches on both, the live smoke test passes on your machine, and `go run ./cmd/dev check` passes.

## Session 6: pages, text layers and page classification

- [ ] `internal/documents` prepares an accepted file: pdfcpu splits it into one-page PDFs, and PDFium reads each page's text with positions and renders it.
  - A page has a text layer when its text holds at least 20 characters that aren't spaces. The threshold is a named constant with a test.
  - Its fingerprint is the SHA-256 of PDFium's rendering at a fixed size, in grayscale. The same fingerprint twice in one claim marks the later page as a repeat of the first; it's recorded, not dropped, and M3 decides what a repeat means. A test pins one page's fingerprint on both CI jobs.
  - In a model request, a page is identified by its file's SHA-256 and its page number (ADR 0014), so the split bytes never need to be stable.
- [ ] Page classification sends one page to `gemini-3.5-flash-lite` and gets its type and printed markers: the patient's name, the document or bill number, the document date and "page x of y", each as printed or null. Any instruction-like text comes back too, at most three snippets of 200 characters.
  - The types follow the packet checklist: claim form, photo ID, admission advice, hospital bill, pharmacy bill, payment receipt, discharge summary, investigation report, prescription, OT notes, implant invoice, bank details, KYC, and other. Week 3's packets don't include every type, so the report names the types it never saw.
- [ ] Grouping is code, not the model. Consecutive pages of one file form one part when they share a type and their bill numbers match or are absent; a page printed as page 1 starts a new one. Unit tests cover a bill split by one misread page, two bills back to back, and one file holding several documents.
- [ ] The packet checklist records which required documents are present: a claim form, a discharge summary, a hospital bill and a payment receipt, plus OT notes for surgery, an implant invoice when an implant is billed, and KYC when the claimed amount is over ₹1 lakh. It is scored against the truth's list of documents. Asking for what's missing is week 4.
- [ ] Code scans each page's text against a short list of instruction-like phrases and records the hits beside the model's.
- [ ] Record classification on the dev half: about 350 calls, within one day's free limit of 500 at 15 a minute, for $0.

Done when classification and grouping run on the dev half, recorded once and replayed in tests, the grouping tests pass, the pinned fingerprint matches on both CI jobs, and `go run ./cmd/dev check` passes.

## Session 7: line-item extraction and its checks

- [ ] Each hospital or pharmacy bill is extracted in ranges of up to four pages, one PDF per call, merged by pdfcpu. A line's page is a number within the PDF sent, from an enum of that PDF's pages in the request's schema, and code maps it to the file's page, so the model never handles the file's page numbers.
- [ ] The schema, `line_items.v1` ([ADR 0015](../adr/0015-model-copies-printed-values-code-converts.md)), holds everything as printed, or null, with every property required.
  - The bill: its number, date, patient name, hospital name and totals (gross, discount, net), plus a list for instruction-like text.
  - Each line: page, service date, description, quantity, unit price and amount.
  - Lines are charges only. Subtotals, "carried forward" rows and payments are left out, and discounts go in the totals.
- [ ] Code parses what was printed, as ADR 0015 sets out: amounts into `int64` paise with overflow checks, dates day first with a two-digit year within a year of the admission, quantities into whole numbers (a fraction is kept as printed and flagged), and descriptions NFC-normalized, stripped of control characters and capped at 200 characters, with the cut recorded. The parsers get table tests and fuzz tests near the `int64` limits.
- [ ] Checks on every line and bill, each recorded with its result:
  - Grounding: each amount and unit price must equal, as a parsed value, a number in that page's text, so `1,23,456.00` matches `123456`. A page with no text layer can't be grounded yet; week 4 adds two-pass agreement.
  - Arithmetic: quantity times unit price must equal the amount, exactly.
  - Totals: once a bill's ranges are stitched in page order, its lines must add up to the gross, and gross less discount to the net. The totals come from the range that holds the bill's last page, and two ranges printing different totals is a failure.
- [ ] A schema failure gets one re-ask that carries the validation error. A totals mismatch gets one re-extraction of that bill, naming both figures. Each is a new request with its own hash. A second failure marks the step failed or flags the lines, keeping both figures.
- [ ] A `max_tokens` stop splits the page range in half and tries again, down to one page, instead of raising the cap.
- [ ] Iterate on the prompt with five dev packets, then record the dev half once. At `gemini-4-hydrogen`'s 20 requests a day that takes several days, so the recording is resumed each day until no cassette is missing; session 8 can start on the packets already recorded.

Done when extraction and its checks run on the dev half, recorded once and replayed in tests, the parsers' fuzz tests run clean, and `go run ./cmd/dev check` passes.

## Session 8: the extraction eval and the first scored run

- [ ] `go run ./cmd/evalrunner extraction` renders the set, then runs preparation, classification, grouping, extraction and the checks. It calls the same functions week 4's activities will call, runs in `replay` by default, and refuses the test half without `--release`.
- [ ] Matching, per bill, is deterministic. First, an extracted line matches a true line on the same page with the same amount, choosing by description when several do. Then the rest match by descriptions at least 0.8 alike (Jaccard similarity of their lower-cased word tokens), best first, with ties broken by position. Each true line matches once; leftovers count as missed or invented lines.
- [ ] Metrics, for golden packets, seeded packets and all of them, and per bill layout:
  - page-type accuracy, with a confusion matrix; the share of parts grouped exactly right; the checklist's accuracy;
  - line recall and precision, and exact-match rates for amount, date, quantity and unit price on matched lines;
  - bills whose lines add up, amounts grounded, and silent errors: a wrong amount that passed both grounding and the totals check;
  - schema failures, re-asks and range splits;
  - tokens and US dollars per page and per packet for each job, and p50 and p95 latency in live and record runs.
- [ ] Before a `record` or `live` run, the harness estimates its cost and refuses to start above `--max-usd`, $10 by default, and estimates its calls against what is left of each model's free daily limit. During the run it stops when the money spent reaches the cap or a limit is reached. On the free tier the cost is $0, so the cap matters only once a paid provider is added.
- [ ] Reports go to `evals/reports/extraction/` as Markdown and JSON, named by date and the commit they ran on (marked `-dirty` if the tree had changes), with the models, prompt hashes, generator version and any cassette misses. CLAUDE.md's rule 9, added with this plan, forbids editing a spec, truth, a cassette or a report by hand to improve a number.
- [ ] Commit the dev half's cassettes, a few megabytes, with the first report. CI replays the extraction eval on every pull request, and fails on a cassette miss or on any metric that differs from the latest committed report.

Done when the first report on the dev half is committed, CI replays it to the same numbers with no key, no test-half packet has gone to a model, and `go run ./cmd/dev check` passes. Its numbers are a first look, and the first evidence of Gemini's accuracy (ADR 0016); week 4 sets the baseline.

## Not this week

- **Week 4:** the discharge-summary facts (diagnosis, procedure, accident, onset and listed conditions); degraded scans, with two-pass agreement on pages with no text layer, using sample indexes 0 and 1; row grounding, so an amount must sit on its description's printed row; the workflow's prepare, classify and extract activities, with migration 000003 and the model-call task queue; asking for missing documents; `datagen upload`; a batch API for full runs, once a paid tier makes one available (Gemini's free tier has none); the baseline in `evals/thresholds.yaml` with tolerances; and the M2 gate.
- **M3:** normalizing lines into the engine's categories, including intensivist and critical-care nursing as `icu-charges` (Definition 24) and ambulance ties ([ADR 0012](../adr/0012-road-ambulance-limit-before-treatment-limits.md)); the full transcription of Annexure A's item text; deduplicating across bills; the agent; calling the engine; routing flags and instruction-like text to review; and workflow versioning with replay tests.
- **Later:** flagging the same file across claims; sweeping rejected and abandoned uploads out of quarantine; the live events endpoint; templates for admission advice, investigation reports, prescriptions, bank details and KYC; the 30-packet adversarial set; and growing the synthetic set to 200 packets by M5.
- No real documents, ever ([ADR 0006](../adr/0006-synthetic-data-only.md)).

## Done by the end of week 3

- A packet uploaded through the API ends with every file accepted, or rejected with a reason, and the claim submitted, each step in the claim's audit chain, whose hashes recompute from the stored rows.
- 100 packets render byte for byte the same on every run and on both OSes, their truth checked against their own text and the golden packets' facts against the checked figures, split into dev and test halves for good.
- Page classification and line-item extraction run on the dev half through one model layer that records and replays, and CI replays them with no key.
- The first extraction report on the dev half is committed and reproduced by CI, and `go run ./cmd/dev check` passes.

## Review notes, 24 November

A review of the draft against the repo, and of its library claims against each library's source, changed these points.

| Change | Where | Why |
| --- | --- | --- |
| Split pages and ranges are identified by their file's SHA-256 and page numbers, and repeats by a hash of PDFium's rendering | Decide first; sessions 6 and 7; ADRs 0013 and 0014 | pdfcpu hashes `time.Now()` into every file's `/ID`, so byte-stable splitting isn't possible |
| PDFium decides `encrypted`, by the security handler | Session 2 | pdfcpu opens a PDF with an empty user password without asking |
| PDFium's sandbox is configured, and pdfcpu sees only files PDFium accepted | Session 2; ADR 0013 | go-pdfium's defaults mount the host's root and set no memory limit, and pdfcpu parses natively |
| The claim's row is locked first, before any check | Decide first; sessions 1 and 2 | Complete and submit could both commit, and outbox IDs are handed out at insert, not commit |
| A check out of retries rejects the file with `check_failed` | Decide first; session 2 | A draft claim can't be `failed` under the claims table's constraints |
| Migration 000002 replaces the `uploaded_at` constraint and adds `image_too_large`, `check_failed` and `pdf_sha256` | Data | The old constraint stops an accepted file keeping its upload time, and two outcomes had no reason code |
| The audit hash has a canonical encoding, recomputed from stored rows in a test | Session 1 | `jsonb` re-orders keys and Postgres keeps microseconds; the foreign key checks links, not hashes |
| SeaweedFS runs in CI's Database job | Session 1 | The S3 tests would skip in CI and the session would look done |
| The signed content length is enforced | Decide first; session 1 | Checked: SeaweedFS 4.53 rebuilds it from the body |
| At most 50 files per claim | Session 1 | Bounds storage and the workflow's history |
| The generator is two sessions, and `datagen upload` moves to week 4 | Sessions 3 and 4 | Too much for one sitting |
| OT notes and implant invoice templates | Session 4 | Without them every surgery packet fails the checklist |
| Annexure item names come from a short table in the generator | Session 3 | Week 2 left the transcription to M3, and the published pack can't change |
| Seeded specs are committed, and the split file is append-only and pinned | Decide first; session 4 | Regenerating specs or re-sorting the split would move scenarios |
| Metrics are reported for golden and seeded packets separately | Decide first; session 8 | Golden packets are short and shaped by engine edge cases |
| Thinking is pinned and off, no fallbacks, and the estimate is $6 to $8 a pass | Decide first; session 5 | Sonnet 5.5 thinks at effort `high` by default, and a fallback answers with an unevaluated model |
| `record` calls only for misses, a sample index joins the hash, and the worker is `live` only | ADR 0014 | Cost, week 4's two passes, and no cassettes from real data |
| CI replays on every pull request and compares with the committed report | Session 8 | A path filter misses go.mod bumps, and "green" has to mean the same numbers |
| The cost cap also stops a run in progress | Session 8 | An estimate can be wrong |
| A line's page is numbered within the PDF sent | Session 7 | Removes a mapping the model could get wrong |
| The self-check covers every printed string | Session 3 | Amounts alone wouldn't catch a wrong date or description |
| The dev tool passes the key from `.env`, and the spend limit is a workspace's | Decide first | `go run` doesn't read `.env`, and Console spend limits are set per workspace |
| A listed line may print its own description, with `matched_by` (25 November, in session 3) | Session 3 | Golden cases 21 and 31 match Annexure A items without printing the item's name |

## Sources

Checked on 24 November 2026. Library facts were read in each library's source at the release named.

- [pdfcpu v0.16.4](https://github.com/pdfcpu/pdfcpu/tree/v0.16.4): `pkg/pdfcpu/crypto.go` (`fileID` hashes `time.Now()`), `pkg/pdfcpu/info.go` (`ensureInfoDict` and `PreserveInfoDict`), `pkg/pdfcpu/read.go` (`ErrWrongPassword`). No EXIF handling anywhere in the repository. Apache-2.0.
- [go-pdfium v1.21.5](https://github.com/klippa-app/go-pdfium/tree/v1.21.5): `README.md` (WebAssembly mode, text with positions, rendering, security handler revision), `webassembly/webassembly.go` (`Config`; the default mounts and random source). MIT; PDFium is Apache-2.0.
- [wazero](https://github.com/wazero/wazero): `config.go` (`WithMemoryLimitPages`, `WithCloseOnContextDone`, `WithCompilationCache`). Apache-2.0.
- [gopdf v0.38.6](https://github.com/signintech/gopdf/tree/v0.38.6): `gopdf.go` (the trailer's `/ID` only with protection; `SetNoCompression`), `pdf_info_obj.go` (`CreationDate` only when set), `pdf_protection.go` (the only clock read), `map_of_character_To_glyph_index.go`, `unicode_map.go`. MIT.
- [Noto Sans sources](https://github.com/notofonts/latin-greek-cyrillic): `sources/NotoSans.glyphspackage/glyphs/rupeeI_ndian.glyph`, unicode 8377 (U+20B9). OFL-1.1.
- [SeaweedFS 4.53](https://github.com/seaweedfs/seaweedfs/blob/4.53/weed/s3api/auth_signature_v4.go): a signed `content-length` is rebuilt from the request's actual length, in the path presigned URLs share.
- [aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2): `aws/signer/internal/v4/headers.go` (`Content-Type` is always signed) and `aws/signer/v4/v4.go` (`content-length` is signed when the request has one).
- [anthropic-sdk-go v2.14.0](https://github.com/anthropics/anthropic-sdk-go/tree/v2.14.0): `message.go` (`OutputConfigParam`, `JSONOutputFormatParam`, `Base64PDFSourceParam`), `option/requestoption.go` (`WithMaxRetries`).
- [Structured outputs (Claude API docs)](https://platform.claude.com/docs/en/build-with-claude/structured-outputs): at most 24 optional and 16 union-typed parameters per request; refusals and `max_tokens` stops; enum capitalization; no string-length or numeric constraints.
- [PDF support (Claude API docs)](https://platform.claude.com/docs/en/build-with-claude/pdf-support): no encrypted PDFs; at most 600 pages a request, or 100 below a 1M-token context; 1,500 to 3,000 text tokens and at most 1,568 image tokens a page.
- [Pricing (Claude API docs)](https://platform.claude.com/docs/en/about-claude/pricing): Haiku 5.5 and Sonnet 5.5 prices, Haiku 5.5's tier above 100,000 prompt tokens, Sonnet 5.5's cache reads at 0.05 of input, and the Batch API's 50% discount.
- Gemini, checked on 27 November 2026, for [ADR 0016](../adr/0016-gemini-free-tier-for-development.md): AI Studio's rate-limit page (signed in, read by Sai) for the free tier's per-model limits; [rate limits](https://ai.google.dev/gemini-api/docs/rate-limits) (per project, daily reset at midnight Pacific); [pricing](https://ai.google.dev/gemini-api/docs/pricing) (free-tier models, and free-tier content used to improve Google's products); [thinking](https://ai.google.dev/gemini-api/docs/thinking) (levels per model; `minimal` unsupported on Gemini 4); [structured output](https://ai.google.dev/gemini-api/docs/structured-output) (no numeric schema limits; complexity from optional properties, enums and `anyOf`); [google.golang.org/genai v1.79.1](https://pkg.go.dev/google.golang.org/genai@v1.79.1). The Claude sources above stay for the adapter that waits for a budget.
- [Architecture and design plan](architecture.md): M2's deliverables, the packet checklist and the extraction design this week follows.

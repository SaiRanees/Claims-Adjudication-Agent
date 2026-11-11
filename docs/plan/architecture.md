# Claims Adjudication Agent — Architecture & Design Plan

Copied from the plan doc on 11 November 2026, with its diagrams written out as text. When the design changes, change this file in the same pull request.

## Goals, scope and non-goals

v1 adjudicates one claim type end to end: a reimbursement claim for a hospital stay under one standard health policy. It returns a line-by-line payable amount, with a policy clause behind every rupee deducted.

**What v1 does**

- Accepts a claim packet as PDFs or photos: discharge summary, itemized final bill, pharmacy and lab bills. Policy and member data come from seeded policy tables, never from an uploaded schedule.
- Extracts every line item, classifies it, and checks eligibility: policy active, member covered, waiting periods, exclusions.
- Computes the payable amount in a deterministic rules engine. The model never does arithmetic.
- Sends anything uncertain, inconsistent or high-value to a human reviewer, and never denies a claim on its own.
- Records every step in an append-only audit log.

**Scope decisions**

- **Policy:** start with Arogya Sanjeevani, IRDAI's standard product. Insurers sell it with common features and exclusions ([source](https://www.money9.com/news/insurance/all-you-need-to-know-about-arogya-sanjeevani-health-insurance-policy-42385.html)), so one rule pack covers many insurers. A retail policy can be added later as a second rule pack.
- **Claim type:** reimbursement first. Cashless pre-authorization is a stretch goal, because insurers must decide those requests within one hour ([source](https://upstox.com/news/personal-finance/insurance/how-long-does-cashless-health-insurance-claim-authorisation-take-latest-irdai-rules-and-data/article-185553)).
- **Data:** synthetic claims and public policy wordings only. No real patient documents, and nothing from any employer's or client's systems.

**Non-goals for v1**

- Integration with real insurer, TPA or hospital systems, and payments.
- Fraud-scoring models (rule-based red flags only).
- Regional-language documents.
- Advice to policyholders.
- Pre- and post-hospitalisation expense claims.
- Judging medical necessity or reasonable charges. Package-rate bills and bill-level discounts go to review.
- Reopening a decided claim after an appeal or grievance.

**Success targets** (initial; tune after the first eval run)

| Metric | Target | Why it matters |
| --- | --- | --- |
| Payable exactly matches ground truth | 95% or more of held-out test claims | The number everyone checks |
| Deductions carrying a valid clause reference | 100% | Unexplained cuts are the core complaint |
| Silent errors: wrong payable, auto-finalized, never flagged | 0 observed on the test set | The one failure that must never ship |
| Line-item category accuracy | 92% or more | Every rule downstream depends on it |
| p95 time for a 30-page packet | 5 minutes or less | Keeps the demo interactive |
| Model cost per claim | Under a budget set after the first run | Enforced per claim, not just reported |

Zero silent errors on 140 held-out claims shows the true rate is probably under about 2% (the rule of three), not that it is zero. Report it that way.

## System overview

A claim flows from the web app through a Go API into a durable Temporal workflow, whose workers call the model and the rules engine.

```text
Web app (React + TypeScript)      uploads, review queue, audit trail
  │
  ▼
API service (Go)                  auth, idempotency keys, upload URLs
  │  the change and its outbox row, in one transaction
  ▼
Postgres                          claims, line items, audit log, outbox
Object store                      original PDFs, private
  │  unpublished outbox rows
  ▼
Outbox relay (Go)                 starts and signals workflows
  │
  ▼
Temporal                          one durable workflow per claim: timers, retries, review pause
  │  activities
  ▼
Workers (Go)                      extraction ──▶ agent loop ──▶ rules engine
  │                               parallel,      typed tools,    pure Go, paise,
  │                               schema-checked proposes only   decides the payable
  │  every model call, through internal/llm
  ▼
Model APIs, outside the system    Claude first, swappable

Workers read and write Postgres and the object store.
Every service ──▶ OTEL Collector ──▶ Grafana (Tempo, Prometheus, Loki)
```

Only workers call the model, and only the rules engine produces amounts. The web app and API never touch the model, and the API never calls Temporal directly: every message to a workflow goes through the outbox.

**Key choices**

- **Temporal, not a home-made queue:** each claim gets durable state, a timer for its settlement deadline, and a signal for the human decision, all built in.
- **Transactional outbox for every message to a workflow:** starts, added documents and review decisions commit with their database change. The relay delivers them at least once, and the workflow drops repeats by event ID.
- **IDs through Temporal, data in Postgres:** activities pass claim and document IDs, never extracted content. Temporal keeps every activity input and output in the workflow history, which is capped at 2 MB a payload and 50 MB a workflow ([source](https://docs.temporal.io/best-practices/worker)), and patient data stays out of it.
- **A separate task queue for model calls:** its workers cap concurrency and calls per second, so a burst of claims waits in the queue instead of hitting the provider's rate limit.
- **Engine as a library inside the workers:** no network hop, trivial to test, versioned with the rule packs.
- **Object storage for originals:** Postgres keeps only hashes and keys.

## Claim lifecycle and durable workflow

Each claim is one Temporal workflow with the ID `claim-<id>`. It either reaches a decision through the engine or stops at a person; no path finalizes a flagged claim.

```text
1. Claim submitted                 starts the 15-day settlement timer
2. Prepare and validate packet     split pages, classify, cross-check
     Packet complete? no ──▶ ask for missing documents (waits for an upload or the deadline)
                             └── documents added ──▶ back to 2
3. Extract documents               one activity per document
4. Normalize and dedupe            categories, cross-bill duplicates
5. Agent classifies lines          clause IDs and confidence per line
6. Rules engine computes payable   same input, same rupees
     Review needed? yes ──▶ human review (paused until a decision signal)
                            └── overrides, then recompute ──▶ back to 6
7. Finalize and notify             letter, audit events, settlement
```

Each step runs as a Temporal activity; after a crash, the workflow resumes from the last finished step.

Missing documents and every raised flag leave the main path, and come back only through a new upload or a person's decision.

**Workflow rules**

- Starting the workflow twice is a no-op, because the ID is fixed per claim.
- Workflow code stays deterministic: no clock, randomness or I/O inside it. All of that lives in activities.
- Every activity is idempotent. Results are stored by claim, step and request hash and reused on retry, so a crash never pays for a model call twice.
- Workflow code drives the agent loop: each model turn and each tool call is its own activity, and the conversation is stored in Postgres. A crash at turn ten resumes at turn ten.
- Retries use exponential backoff with an attempt cap. Errors that can't succeed on retry, such as an encrypted PDF, go straight to review or back to the submitter for a new file.
- The API validates and stores a review decision with an outbox row in the same transaction. The relay signals the workflow, which loads the decision by ID and recomputes with the engine.
- The wording requires a settle-or-reject decision within 15 days of claim submission, with interest after that ([source](https://www.godigit.com/content/dam/godigit/directportal/en/downloads/health/policy-wordings-arogya-sanjeevani-policy-go-digit.pdf)). The timer starts at submission and escalates well before the deadline.
- When the wait for missing documents runs out, the claim goes to a reviewer. Nothing closes or rejects it automatically.
- Temporal versioning keeps in-flight claims on the code path they started with, and CI replays recorded histories before each deploy.

**What sends a claim to review** (checked in code after the engine runs; any one is enough)

- A proposed denial: an exclusion, a waiting period, a pre-existing disease, or a claim with nothing payable.
- A failed cross-check: lines don't add up to the bill total, an amount is missing from the page text, or names and dates don't match the policy.
- A disagreement between the agent and the code-side item lookup, or a line the agent flagged.
- A billed or payable amount above a set value, or a payable within a small margin of a limit.
- A case the engine doesn't model: a package-rate bill, a bill-level discount, a line with no date in a stay with more than one room rate, or a known conflict between wording and regulation.
- A step that hit its retry, turn or token limit.

IRDAI's 2024 master circular bars an insurer from repudiating a claim without approval from its Product Management Committee or Claims Review Committee ([source](https://rsdebate.nic.in/bitstream/123456789/758801/1/PQ_267_11022025_U874_p297_p299.pdf)), so a denial always belongs to people. The model's confidence score is one more input, calibrated on the dev set; it never clears a claim by itself. Auto-finalizing stays off, so every claim gets a reviewer, until the test-set numbers justify turning it on for clean claims.

**Packet checklist,** from the wording's reimbursement document list: claim form, photo ID, the doctor's advice to admit, itemized original bills, payment receipts, discharge summary, investigation reports with prescriptions, OT notes for surgery, implant invoice where used, bank details, and KYC for claims above ₹1 lakh. Bills must be in the insured person's name ([source](https://www.godigit.com/content/dam/godigit/directportal/en/downloads/health/policy-wordings-arogya-sanjeevani-policy-go-digit.pdf)).

**Stored states:** Draft (created, documents still uploading), Submitted, Validating, Awaiting documents, Extracting, Adjudicating, In review, Decided and Closed, plus Rejected for an unusable packet and Failed when retries run out and an operator must step in. In code: `draft`, `submitted`, `validating`, `awaiting_documents`, `extracting`, `adjudicating`, `in_review`, `decided`, `closed`, `rejected`, `failed`.

## Agent design

The agent proposes; code decides. It reads extracted facts, looks things up through typed tools, and submits a classification for every line. The rules engine turns that into rupees, and code validates every field before anything is stored.

A loop, not a single call, because which clause or item list to check depends on what each line turns out to be. The evals keep a single-call baseline, so the loop has to earn its extra cost.

**Four model jobs**

| Job | Input | Output | How it's called |
| --- | --- | --- | --- |
| Page classification | One page | Document type, patient name, dates | JSON outputs, smaller model |
| Line-item extraction | A page range of one document | Line items with page, service date, description, quantity, unit price and amount; bill totals and discounts | JSON outputs, strict schema |
| Claim-level facts | Discharge summary | Diagnosis, procedure, accident or not, matches to listed conditions and treatments | JSON outputs, strict schema |
| Adjudication | Normalized claim and rule-pack summary | Category, decision, clause IDs and confidence per line | Agent loop with strict tools |

**The loop, hand-written in Go**

1. Send the claim summary, the open questions and the tool list.
2. Run each tool call: validate its input, execute, append the result. Tool errors go back as error results, not crashes.
3. Stop on `submit_adjudication`, after 12 turns, or when the claim's token budget runs out. The last two send the claim to review.
4. Validate the submission in code, then recompute the payable with the rules engine.

Workflow code drives the loop. Each model turn and each tool call is a separate Temporal activity on the model-call task queue, and the messages live in Postgres, so only IDs pass through Temporal.

**Tools** (strict schemas; only the last two write anything)

| Tool | Returns or does |
| --- | --- |
| `get_claim_facts` | Member, dates, diagnosis, and line items with IDs |
| `get_clause(clause_id)` | Verbatim clause text and page from the onboarded wording |
| `search_clauses(query)` | Best-matching clauses by section and keyword |
| `lookup_item_list(description)` | Match against the wording's Annexure A lists: not payable, or folded into room, procedure or treatment costs |
| `check_waiting_period(condition)` | Date check in code against first inception, continuity credit and the waiting-period lists. Matching the diagnosis to a listed condition is the model's call, and a reviewer confirms it before any denial |
| `flag_for_review(line_ids, reason)` | Marks lines for a human |
| `submit_adjudication(result)` | The final answer; ends the loop |

The agent has no tool that shows a payable amount. Categories shouldn't depend on the payout, and a payout view would give injected text something to aim at.

The same tools are served over MCP with the [official Go SDK](https://github.com/modelcontextprotocol/go-sdk), so you can call them from any MCP client while debugging.

**Guardrails live in code, not in the prompt**

- Every deduction must cite a clause ID that exists in this policy version. An unknown ID is rejected, with one retry.
- The engine recomputes the payable from the submitted classifications. Any number the model writes is ignored.
- Item-list matching also runs in code. Where the code lookup marks a line not payable, the agent can't mark it payable; the disagreement goes to review.
- Text copied from documents, such as descriptions and diagnoses, is length-capped and passed as data the prompt marks as untrusted.
- Category values are compared case-insensitively, because structured outputs don't guarantee enum capitalization ([source](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)).
- A `refusal` or `max_tokens` stop counts as a failed step, since the output may not match the schema then ([source](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)).
- Schemas stay lean: one request allows at most 20 strict tools and 24 optional parameters ([source](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)).

**Documents and citations**

- Preparation runs in code before any model call: photos become PDFs, pages are split and de-duplicated by hash, and a text layer is pulled where one exists (for example with pdfcpu and Poppler).
- With the Claude adapter, PDFs go straight to the API, which reads each page as an image plus its extracted text ([source](https://platform.claude.com/docs/en/build-with-claude/pdf-support)).
- On Claude, a request holds at most 100 pages below a 1M-token context window, and encrypted PDFs aren't accepted ([source](https://platform.claude.com/docs/en/build-with-claude/pdf-support)). So documents go in page ranges, and encrypted files are rejected at intake.
- Page ranges also keep a long pharmacy bill from overflowing the output limit. Tables that run across pages are stitched in code and checked against the bill total.
- Grounding check: where a page has a text layer, every extracted amount must appear in that page's text. For image-only pages, two independent extraction passes must agree. A miss sends the line to review.
- Clause IDs come from a clause table built once per wording at onboarding and checked by a person. IDs stay stable and testable.
- Claude's citations feature can add page-exact quotes to the settlement letter. It can't be combined with structured outputs in one request ([source](https://platform.claude.com/docs/en/build-with-claude/citations)), so the letter is its own call.

**Cost and models**

- The model for each job is configuration: provider and model, recorded on every call. Changing either requires a passing eval run.
- With Claude, the policy wording is uploaded once with the Files API and reused with prompt caching ([source](https://platform.claude.com/docs/en/build-with-claude/pdf-support)).
- Inside workflow activities, each provider SDK's own retries are off (`option.WithMaxRetries(0)`). Claude's Go SDK otherwise retries twice by default ([source](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go)), and stacked retries multiply cost.

**Swappable model providers**

- Every model call goes through one Go interface in `internal/llm`, with one adapter per provider. Claude's adapter comes first; a second provider's adapter is a stretch goal that proves the seam.
- Each adapter declares what it supports: native PDF input, schema-constrained output, strict tool inputs, citations, prompt caching. The core never assumes any of them.
- A missing capability falls back in code: pages rendered to images, JSON validated and re-asked, letter quotes taken from the clause table.
- Prompts are versioned per job and per provider, since wording tuned for one model may not suit another.
- The eval suite runs per provider and model, so a switch is a measured decision: accuracy, silent errors, cost and latency side by side.

## Rules engine

All money math runs in one pure Go package. Claim facts and a versioned rule pack go in; a payable amount and one adjustment per rule applied come out. No I/O, no model calls, and the same answer every time.

**Design rules**

- Money is `int64` paise and percentages are basis points. No floats on the money path.
- Rounding happens once per line, with one documented rule (half up, to the paisa), covered by tests. Claim-level amounts such as co-payment are spread across lines by largest remainder, so lines always add up to the total.
- Each adjustment records line ID, rule ID, clause ID, inputs and amount. That record is the explanation reviewers and letters show.
- The rule pack is chosen by the policy version in force on the admission date, then pinned on the claim, so re-running an old claim reproduces the old answer.

**Rule pack: one YAML file per policy wording version.** The first one encodes Go Digit's wording of Arogya Sanjeevani ([wording](https://www.godigit.com/content/dam/godigit/directportal/en/downloads/health/policy-wordings-arogya-sanjeevani-policy-go-digit.pdf)):

| Rule | What the wording says | Clause |
| --- | --- | --- |
| Room, boarding, nursing | 2% of sum insured, up to ₹5,000 a day | C.I.1 (i) |
| ICU / ICCU | 5% of sum insured, up to ₹10,000 a day | C.I.1 (ii) |
| Proportionate deduction | Other hospital expenses scale down when the room or ICU rate is over its cap; pharmacy and consumables, implants and devices, and diagnostics are exempt | C.I.1, Note 2 |
| No differential billing | Proportionate deduction doesn't apply | C.I.1, Note 3 |
| Cataract | 25% of sum insured or ₹40,000 per eye, whichever is lower | C.I.3 |
| Listed modern treatments | Up to 50% of sum insured | C.I.6 |
| Road ambulance | Up to ₹2,000 per hospitalisation | C.I.1.1 (v) |
| Waiting periods | 30 days initial; 24 or 36 months for listed conditions; 36 months for pre-existing disease | Excl01 to Excl03 |
| Co-payment | 5% of the admissible claim amount | E.II.28 (c) |
| Item lists | Not payable, or folded into room, procedure or treatment costs | Annexure A, Lists I to V |

**Order of operations** (explicit, versioned and tested, because the order changes the answer)

1. Drop items the Annexure A lists mark as not payable.
2. Cap room and ICU charges at their per-day limits.
3. Scale non-exempt hospital expenses by the room-rate ratio of their stay segment.
4. Apply sub-limits: cataract, modern treatments, ambulance.
5. Apply the 5% co-payment.
6. Cap at the sum insured plus cumulative bonus left for the policy year.

Step 3, per line:

```latex
\text{admissible} = \text{billed} \times \min\left(1,\ \frac{\text{admissible room rate per day}}{\text{actual room rate per day}}\right)
```

**Stay segments:** the stay is split into date ranges by room type and daily rate: ward, upgraded room, ICU. Each line is scaled by the ratio of the segment its service date falls in. A line with no date in a stay with more than one rate goes to review.

**Balances across claims:** the sum insured and cumulative bonus left, and what's been used of yearly sub-limits such as cataract per eye, are inputs read from a ledger. The engine stays pure; the ledger is locked and updated when a claim is finalized.

**Worked example:** sum insured ₹5,00,000, so the room cap is ₹5,000 a day. The patient stayed 4 days in an ₹8,000 room at a hospital that bills by room category.

| Line | Billed (₹) | Rule applied | Admissible (₹) |
| --- | --- | --- | --- |
| Room rent, 4 days at ₹8,000 | 32,000 | Capped at ₹5,000 a day | 20,000 |
| Surgeon, OT and consultant fees | 1,00,000 | Scaled by 5,000 ÷ 8,000 = 62.5% | 62,500 |
| Pharmacy | 30,000 | Exempt from scaling | 30,000 |
| Diagnostics | 10,000 | Exempt from scaling | 10,000 |
| Admission kit and other listed items | 2,000 | Not payable | 0 |
| **Subtotal** | **1,74,000** |  | **1,22,500** |
| Co-payment, 5% |  |  | −6,125 |
| **Payable** |  |  | **1,16,375** |

**Where the wording and regulation can disagree**

- IRDAI's 2020 rules tell insurers not to apply proportionate deduction for ICU charges ([source](https://www.business-standard.com/article/companies/out-of-pocket-expenses-of-policyholders-to-fall-after-new-irdai-guidelines-120061200007_1.html)), yet this wording also scales by the ICU rate. The rule pack marks this as a known conflict, and any claim that triggers it goes to review.
- The wording doesn't say whether co-payment comes before or after the sum-insured cap. Record the choice in an ADR, pin it with a golden case, and flag claims near the cap.

**Testing**

- Golden cases are computed by hand and checked in a spreadsheet, never generated by the engine.
- Property tests: payable never exceeds billed or drops below zero; line amounts add up to the total; raising the sum insured never lowers the payable.
- Go's native fuzzing runs on rule-pack inputs to catch panics and overflow.

## Data model and APIs

Postgres is the system of record; Temporal holds only in-flight workflow state. Every table that money depends on is append-only or versioned, so any decision can be replayed.

**Core tables**

| Table | Holds | Key constraint |
| --- | --- | --- |
| `policies`, `members` | Seeded policy data, the source of truth for cover: sum insured, policy periods, first inception and continuity dates, individual or floater | Unique policy number per insurer |
| `policy_balances` | Ledger per policy year: sum insured and cumulative bonus used, sub-limit use per member | Append-only entries; balance row locked at finalize |
| `policy_versions`, `clauses` | Onboarded wording; clause ID, section, text, page | Clause IDs never change once published |
| `rule_packs` | YAML rule pack per wording version, effective dates | One active pack per version and date |
| `claims` | Status, member, admission and discharge dates, pinned rule-pack version, workflow ID | Optimistic-lock `version` column |
| `stay_segments` | Date ranges of the stay by room type and daily rate | Ranges don't overlap and cover the whole stay |
| `documents` | Object-store key, SHA-256, page count, detected type | Unique (claim, SHA-256) blocks duplicate uploads |
| `model_calls` | Every model call: step, request hash, provider and model, prompt hash, tokens, cost, latency, status | Unique (claim, step, request hash), so a retry reuses the stored result |
| `agent_runs`, `agent_messages` | The agent conversation per claim, turn by turn | Turn numbers unique per run |
| `extractions` | Model output per document and page range, schema version, link to its model call | Never updated; re-extraction adds a row |
| `line_items` | Description, category, service date, quantity, unit price, amount in paise, source page, package flag | Amount at least zero; quantity × unit price matches amount |
| `adjudications`, `adjustments` | Versioned results; one adjustment per rule applied, with its clause ID | New version on every recompute or override |
| `review_tasks` | Reason codes, assignee, lease expiry, decision | One open task per claim |
| `audit_events` | Every state change, model call, tool call and human action, as IDs and amounts | Insert-only database role; hash-chained per claim |
| `outbox` | Messages for workflows (start, documents added, review decided) and notifications, each with an event ID | Written in the same transaction as the change |

**Rules that close gaps**

- The app's database role can't `UPDATE` or `DELETE` audit events. Each row stores the hash of the previous row for the same claim, and a verifier job re-walks the chains and alerts on a break.
- Audit events hold IDs and amounts, not names or document text, so a document can be deleted later without breaking the log.
- Creating a claim writes the claim row and an outbox row in one transaction. A relay starts the workflow with ID `claim-<id>`, so a retry can never start a second one. Added documents and review decisions travel the same way.
- Finalizing a claim locks its policy's balance row, re-checks the cap against the current balance, and writes the ledger entry in the same transaction. Two claims on one policy can't both spend the same cover.
- Amounts are `bigint` paise end to end, including in the API.

**REST API (OpenAPI first, Go server and TypeScript client both generated)**

| Method and path | Purpose |
| --- | --- |
| `POST /v1/claims` | Create a claim; needs an `Idempotency-Key` header |
| `POST /v1/claims/{id}/documents` | Get a short-lived upload URL per file |
| `POST /v1/claims/{id}/documents/{docId}/complete` | Confirm an upload so it can be checked |
| `POST /v1/claims/{id}/submit` | Lock the packet and start adjudication; also resubmits after missing documents are added |
| `GET /v1/claims/{id}` | Status, timeline and totals |
| `GET /v1/claims/{id}/events` | Live status as server-sent events |
| `GET /v1/claims/{id}/adjudication` | Line items, adjustments and clause references |
| `GET /v1/review-tasks` | Review queue with filters |
| `POST /v1/review-tasks/{id}/claim` | Take a task under a time-limited lease |
| `POST /v1/review-tasks/{id}/decision` | Approve, correct or override, with reason codes |
| `GET /v1/claims/{id}/audit` | Full audit trail |

**Auth and roles**

- JWT sessions with three roles: submitter, reviewer, admin.
- Maker-checker: nobody reviews a claim they submitted, and admins can't approve claims.
- Per-user token-bucket rate limits on uploads and submissions.

## Review UI

The reviewer screen exists for one job: resolve what was flagged, quickly, without ever typing a payable amount.

**Screens**

- **Queue:** open tasks sorted by settlement deadline, with reason codes, claim value and time waiting.
- **Claim review:** document viewer on the left; line items on the right, each linked to its source page, flagged lines first.
- **Breakdown:** billed, adjusted and payable per line, every deduction expandable to its clause text.
- **Audit trail:** a timeline of every step, model call and human action, with trace links.
- **Admin:** policy onboarding (upload a wording, verify its clause segmentation), rule-pack versions, eval results.

**Override rules**

- Reviewers change inputs, not totals: a misread billed amount or date, a category, an item-list match, a waiting-period decision. The engine recomputes the payable.
- A corrected value is stored next to the model's value, never over it, and feeds the extraction error metrics.
- Every override needs a reason code, and a deduction override needs a clause ID.
- Overrides create a new adjudication version; nothing is edited in place.
- The override rate per reason code is tracked. A rising rate means a prompt or model has drifted.

**Stack:** React and TypeScript on Vite, TanStack Query for server state, the generated API client, and server-sent events for live status.

## Evaluation strategy

Evals gate every merge. Engine tests run on every pull request, and any change to prompts, schemas or model config also runs the model smoke set.

**Datasets** (target sizes)

| Set | Size | Built from | What it tests |
| --- | --- | --- | --- |
| Engine golden cases | 40 | Hand-computed scenarios, checked in a spreadsheet | Rules engine only |
| Synthetic packets | 200 | A generator that renders bills and discharge summaries from scenario specs | Extraction, classification, end to end |
| Degraded scans | 50 | The same packets rotated, blurred, skewed or photographed | Extraction robustness |
| Adversarial packets | 30 | Packets with planted problems, listed below | Guardrails and review routing |

**Dev and test halves:** each model-facing set is split in half by scenario. Prompts and thresholds are tuned on the dev half only. The README reports the test half (100 synthetic, 25 degraded, 15 adversarial), run once per release candidate.

**Planted problems**

- Text in a bill telling the model to approve the full amount.
- An instruction hidden inside a line-item description, not in the page body.
- A non-payable item described so it reads like a medicine.
- The same medicine billed on both the final bill and the pharmacy bill.
- Line items that don't add up to the bill total.
- A patient name that doesn't match the insured member.
- An admission inside the 30-day initial waiting period.
- A room rate just over the cap, and a hospital that doesn't bill by room category.

**Ground truth:** each scenario spec defines the true classifications. The correct amount is the engine's output on those true classifications, and the engine itself is checked against the hand-computed golden cases. Nothing grades itself.

**Metrics** (targets are in Goals)

- Payable exact-match rate, and the distribution of rupee errors.
- Line-item category accuracy; field-level extraction precision and recall for amount, date and quantity.
- Silent errors: a wrong payable with no review flag.
- Review routing: share of claims sent to review, and share of those that needed it.
- Clause validity: share of deductions whose clause actually supports them, model-graded and spot-checked by hand.
- Baseline: the same claims through a single-call classifier with no tools, to show what the loop adds and what it costs.
- Calibration: how often lines at each confidence level were right, on the dev half.
- p50 and p95 latency, and tokens and cost per claim.

**Running it**

- Unit, property and golden tests run on every PR in seconds, with no model calls.
- A 30-claim smoke set from the dev half runs on PRs that touch prompts, schemas or model config, three times per claim to measure variance. Cached extraction results are replayed when only the agent changed. A metric that drops beyond its tolerance fails the build.
- The full suite runs before a release and on demand, not nightly. Page classification and extraction go through the Batch API at half price ([source](https://platform.claude.com/docs/en/about-claude/pricing)). The agent loop is multi-turn, so it runs live.
- Budget before running. Text alone is 1,500 to 3,000 tokens a page ([source](https://platform.claude.com/docs/en/build-with-claude/pdf-support)), so 280 claims of 30 pages need 13 to 25 million input tokens for one extraction pass. At $2 per million input tokens on Claude Sonnet 5.5 ([source](https://platform.claude.com/docs/en/about-claude/pricing)), that is $25 to $50 a pass before page images, output and the agent loop. Keep most synthetic packets to about ten pages.
- Record-and-replay mode caches model responses by request hash, so changes that don't touch the model test deterministically.

## Security, privacy and prompt-injection defense

Treat every uploaded document as hostile input, and every model output as untrusted until code has checked it.

**Prompt injection**

- Extraction calls get no tools. They can only return JSON in a fixed schema, so text in a bill can't trigger an action.
- The adjudication agent sees extracted fields, not pages. Descriptions and diagnoses still come from the document, so they are length-capped, passed as data, and attacked in the adversarial set.
- The agent's only write tools are flag and submit, and it never sees a payable amount.
- Instruction-like text found during extraction raises a red flag and sends the claim to review.
- Final amounts come from the engine, so the model can't write a number or pay beyond the bill and the policy limits. A wrong category can still move the payable inside those limits, which is why item matching also runs in code and disagreements go to review.

**Files**

- Uploads land in a quarantine prefix. A worker checks type by magic bytes, size and page count, rejects encrypted PDFs, and only then attaches the file to the claim.
- Store originals in a private bucket with server-side encryption; upload and view through short-lived presigned URLs.
- Hash every file. The same file twice in one claim is rejected; the same file across claims is flagged.

**Data and privacy**

- v1 uses synthetic data only. If real documents are ever used: written consent, redaction before upload, a review against India's DPDP Act, and a check of the model provider's data-retention terms.
- Each model call gets only the fields its step needs; policy and phone numbers are masked in prompts.
- No document text or personal data in logs. Traces carry IDs, not content.

**Access and secrets**

- Least-privilege database roles: the API can't touch audit history, and workers can't change rule packs.
- Secrets come from the environment or a secrets manager, never the repo; CI scans for leaked keys.
- The project stays fully separate from any employer's or client's code, data and assets.

## Observability and operations

One claim is one trace. The workflow, every activity, every model call and every tool call share a trace ID, and that ID is stored in the audit log.

**Tracing and metrics**

- Claude's Go SDK emits an OpenTelemetry span per API request once a tracer provider is registered ([source](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go)). Tool calls, engine runs and other providers' calls get their own spans.
- Traces, metrics and logs go through an OTEL Collector to Grafana (Tempo, Prometheus, Loki).
- Key metrics: claims by state and time in state, review queue depth and age, auto-finalize rate, override rate, extraction schema failures, tokens and cost per claim, settlement-deadline breaches.
- Alerts: cost per claim over budget, a spike in schema failures, review tasks older than target, failed workflows.

**Environments**

| Environment | Runs | Notes |
| --- | --- | --- |
| Local | Docker Compose: Postgres, SeaweedFS (S3), Temporal dev server, OTEL Collector, Grafana stack, API, workers, web app | One command; seeded with synthetic claims |
| CI | GitHub Actions: lint, `go test -race`, Testcontainers integration tests, Temporal workflow tests, eval gates, image builds | Model calls only in eval jobs |
| Demo | One small VM or a k3s cluster running images from CI | Synthetic data only; a spend limit on the API account |

**Delivery**

- Short-lived branches into main; every merge builds versioned images.
- Database migrations are forward-only (for example with golang-migrate) and run before new code starts.
- Workflow code changes use Temporal versioning, so claims in flight finish on the code they started with.
- Feature flags gate risky behavior. Auto-finalizing without review is off by default.

## Failure modes

Every failure below ends in one of two places: a retry that can't double-charge, or a person. The held-out test set measures how often one slips through anyway.

| Failure | What would go wrong | How the design handles it |
| --- | --- | --- |
| Model API times out or returns 429 or 5xx | Step fails mid-claim | Temporal retries with backoff and an attempt cap, SDK retries off; then a second provider with green evals, or review |
| A burst of claims hits the provider's rate limit | Retries pile up | Model calls run on their own task queue with a concurrency and rate cap |
| Output fails its schema, or stops on refusal or max tokens | No usable result | One re-ask with the validation error; a second failure goes to review |
| Agent cites a clause that doesn't exist | Unsupported deduction | Validator rejects it; one retry; then review |
| Model marks a non-payable item as payable | Overpayment inside the limits | Item matching also runs in code; a disagreement goes to review |
| Line items don't add up to the bill total | Missed or invented lines | Re-extract that document; still off goes to review with both totals shown |
| An amount is misread but the lines still add up | Wrong payable | Amount must appear in the page text; image-only pages need two passes that agree |
| Same charge on the final bill and the pharmacy bill | Double counting | Normalization dedupes by date, description and amount; unclear matches go to review |
| Worker crashes mid-activity | Half-finished step | Results stored by claim, step and request hash; a retry reuses them, and the agent loop resumes at its turn |
| Review decision saved, workflow never told | Claim stuck in review | Decision and outbox row commit together; the relay retries; the workflow drops repeats |
| Two claims on one policy finalize together | Cover spent twice | Balance row locked and re-checked at finalize |
| Workflow history grows too large | Workflow terminated | Only IDs pass through Temporal; content stays in Postgres and object storage |
| Duplicate submission or upload | Two claims for one stay | Idempotency key on create, file hash per claim, flag on same member, hospital and dates |
| Rule pack edited while a claim is in flight | Inconsistent answer | Claim pins its rule-pack version at submission; edits create a new version |
| Workflow code deployed with claims in flight | Replay breaks | Temporal versioning, plus replay tests on recorded histories in CI |
| Reviewer never acts | Claim misses its deadline | Timer escalates early, queue sorts by deadline, breaches are a metric |
| Two reviewers open the same task | Conflicting decisions | Time-limited lease plus optimistic locking on the claim |
| Agent loops or burns tokens | Cost runaway | Turn limit and per-claim token budget; hitting either sends the claim to review |
| Injected instructions in a document | Manipulated decision | No tools at extraction, capped data-only text fields, no payable shown to the agent, engine-computed amounts, red-flag routing |
| Encrypted, oversized or corrupt PDF | Pipeline error | Rejected in quarantine with a clear reason and a request for a new file |
| Scanned page with no text layer | Weak extraction | Read as an image; two extraction passes must agree, or the line goes to review |
| Policy dates read in the wrong time zone | Waiting-period mistakes | Policy and admission dates stored as date-only values in IST |
| Model version changes behavior | Silent regression | Model IDs pinned in config; an upgrade needs a green eval run |

## Repo structure and build plan

One Go module plus a web app, built in six milestones over roughly eight part-time weeks. The engine comes first, because every later step is tested against it.

```text
claims-agent/
├── cmd/
│   ├── api/          REST API
│   ├── worker/       Temporal workers: extraction, agent, engine
│   ├── relay/        outbox relay
│   ├── evalrunner/   eval suite CLI
│   ├── datagen/      synthetic claim generator
│   └── mcpserver/    agent tools over MCP, for debugging
├── internal/
│   ├── rules/        the engine: pure, no I/O
│   ├── agent/        loop, tools, validators
│   ├── llm/          provider interface, adapters, call records
│   ├── extraction/   schemas, page classification
│   ├── workflow/     Temporal workflows and activities
│   ├── claims/  documents/  policy/  review/  audit/
│   └── platform/     config, database, OTEL, auth
├── rulepacks/arogya-sanjeevani/godigit-2024.yaml
├── prompts/          versioned templates, embedded with go:embed
├── evals/            datasets, thresholds.yaml, reports
├── web/              React + TypeScript app
├── deploy/           k3s manifests (compose.yaml sits at the root)
└── docs/adr/         architecture decision records
```

Each milestone ends in something you can show, and its gate must pass before the next starts.

| When | Milestone | Deliverables | Exit gate |
| --- | --- | --- | --- |
| Week 1 | M0 · Foundations | Repo, Compose stack, CI, schema migrations, OpenAPI spec | One command starts it |
| Weeks 1–2 | M1 · Rules engine | Rule pack for one wording, golden cases, a CLI to run them | All golden cases pass |
| Weeks 3–4 | M2 · Intake and extraction | Uploads, extraction, synthetic data v1, baseline evals | Extraction baseline set |
| Weeks 5–6 | M3 · Agent and workflow | Temporal workflow, agent loop and tools, review pause | 50 claims end to end |
| Week 7 | M4 · Review UI and ops | Review queue, audit view, Grafana dashboards, alerts | Reviewer can override |
| Week 8 | M5 · Hardening and launch | Adversarial set, CI eval gate, README metrics, demo video | Goals met or explained |

Each gate is a demo you can record. If a gate slips, cut scope from the next milestone, never from the evals.

**Cut first if time runs short:** the second model provider, the MCP server, citations in the settlement letter, k3s, and log storage in the demo.

**Never cut:** the engine's golden cases, the outbox and workflow, the review queue, and held-out test numbers.

This week's tasks are in [week-1.md](week-1.md).

## Risks and open decisions

**Risks**

| Risk | Likelihood | Mitigation |
| --- | --- | --- |
| Policy rules are subtler than the wording reads | High | One standard product first; every ambiguity becomes an ADR and a golden case |
| Eight part-time weeks is tight for this scope | High | The cut-first list; no new feature enters without one leaving |
| The synthetic-claim generator takes longer than planned | High | Start it in M1 with the engine; scenario specs double as golden cases |
| Scope creeps into a full insurance platform | High | The non-goals list, and every milestone ends in a demo |
| Synthetic bills look too clean to prove anything | Medium | Degraded-scan set, several hospital bill templates, handwritten-style notes |
| Model cost grows with page count | Medium | Smaller model for page classification, cached wording, per-claim budget |
| Eval runs cost more than expected | Medium | Ten-page packets, cached extraction, full suite only before a release |
| A regulatory reading turns out wrong | Medium | Framed as a demo, not advice; the regulatory overlay kept separate and cited |

**Open decisions**

- [x] First policy: Arogya Sanjeevani, decided 10 November. A retail policy can follow later as a second rule pack.
- [ ] Check proportionate-deduction scope against IRDAI's 2024 master circular on health insurance itself; secondary sources disagree on whether it narrowed the rule.
- [ ] Choose where co-payment sits relative to the sum-insured cap, and record it in an ADR.
- [ ] Decide which review triggers must stay clean on the test set before auto-finalize is switched on.
- [ ] Pick the retail policy for v2, if one is needed.
- [ ] Set the per-claim cost budget after the first eval run.
- [ ] Decide on demo hosting: one VM or k3s.
- [ ] Name the project.

## Review notes, 10 November

A design review of this plan found the overall shape sound and changed the points below.

| Change | Where | Why |
| --- | --- | --- |
| Every message to a workflow goes through the outbox | System overview, Data model | A decision saved without its signal would strand a claim |
| Only IDs pass through Temporal | System overview | History is size-capped and shouldn't hold patient data |
| Agent loop runs turn by turn as activities | Lifecycle, Agent design | A crash used to restart the whole loop and pay again |
| Review triggers written down; no automatic denials | Lifecycle | "Uncertain" wasn't defined, and regulation puts denials with people |
| Item matching in code; agent sees no payable | Agent design, Security | A wrong category can raise a payout inside the limits |
| Preparation, page ranges and a grounding check | Agent design | Long bills and misread amounts had no control |
| Stay segments and policy-year balances | Rules engine, Data model | Mixed room and ICU stays, and two claims on one policy, were unhandled |
| Reviewers can correct extracted amounts | Review UI | Misread amounts are the likeliest error |
| Dev and test halves, baseline, calibration, cost budget | Evaluation | Tuning and reporting on the same claims overstates results |
| Full suite on demand; Batch API only for single-shot jobs | Evaluation | Nightly runs were costly, and the loop can't run in batch |
| Audit chain per claim, with a verifier | Data model | One global chain forces every insert into a single line |
| Cut-first list and three new risks | Build plan, Risks | Eight weeks had no slack |

## Sources

- [Arogya Sanjeevani Policy, Go Digit: policy wording](https://www.godigit.com/content/dam/godigit/directportal/en/downloads/health/policy-wordings-arogya-sanjeevani-policy-go-digit.pdf)
- [IRDAI proportionate-deduction rules, June 2020 (Business Standard)](https://www.business-standard.com/article/companies/out-of-pocket-expenses-of-policyholders-to-fall-after-new-irdai-guidelines-120061200007_1.html)
- [Cashless authorisation time limits under the 2024 master circular (Upstox)](https://upstox.com/news/personal-finance/insurance/how-long-does-cashless-health-insurance-claim-authorisation-take-latest-irdai-rules-and-data/article-185553)
- [No repudiation without committee approval (Rajya Sabha reply, February 2025)](https://rsdebate.nic.in/bitstream/123456789/758801/1/PQ_267_11022025_U874_p297_p299.pdf)
- [Arogya Sanjeevani as a standard product (Money9)](https://www.money9.com/news/insurance/all-you-need-to-know-about-arogya-sanjeevani-health-insurance-policy-42385.html)
- [Payload and history size limits (Temporal docs)](https://docs.temporal.io/best-practices/worker)
- [PDF support (Claude API docs)](https://platform.claude.com/docs/en/build-with-claude/pdf-support)
- [Citations (Claude API docs)](https://platform.claude.com/docs/en/build-with-claude/citations)
- [Structured outputs (Claude API docs)](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)
- [Pricing (Claude API docs)](https://platform.claude.com/docs/en/about-claude/pricing)
- [Go SDK (Claude API docs)](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go)
- [MCP Go SDK (GitHub)](https://github.com/modelcontextprotocol/go-sdk)

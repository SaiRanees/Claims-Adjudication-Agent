# Week 2 · Rules engine (M1)

By the end of the week, one versioned rule pack encodes Go Digit's wording of Arogya Sanjeevani, the pure engine in `internal/rules` turns a claim's facts into a payable amount with one adjustment per rule applied, and 40 independently checked golden cases pass from a CLI. Nothing touches the database, Temporal or a model this week: the engine is a library, and the workflow starts calling it in M3.

Progress, 18 November: the plan is approved with changes, and ADRs 0009 and 0010 are accepted. Session 1 is done: the rule pack loads, validates and is selected through the policy period. Session 2 is done, with the three fixes from the session 1 review, and its first 10 golden cases are checked.

Progress, 20 November: session 3 is done: steps 1 to 3 are coded, and golden cases 01 to 24 are checked and pass. Session 4 codes every step but eligibility; all 32 golden cases pass in full, and cases 25 to 32 wait for their check.

## Decide first

- [x] Co-payment after the cap ([ADR 0009](../adr/0009-cap-before-co-payment.md)). Step 5 caps the admissible total at the cover left. Step 6 takes 5% of the capped amount, with `Percent` once and then `Allocate`. E.II.28(c) applies the 5% to the amount "admissible and payable", and Definition 10 says a co-payment doesn't reduce the sum insured, so the ledger records only what's paid. The two orders can differ only when the admissible amount exceeds the cover left. Every such claim gets a `cover_exceeded` flag carrying the co-pay-first payable after the same bill discount, which is never lower than the payable.
- [x] ICU and proportionate deduction ([ADR 0010](../adr/0010-cap-icu-charges-flag-icu-scaling.md)). ICU charge lines are capped per day and never scaled. Scaling other lines by an ICU ratio follows the wording (C.I.1 Note 2) under its own rule ID, and raises `icu_ratio_scaling`.
  - IRDAI's 2020 circular (IRDAI/HLT/REG/CIR/151/06/2020, para 7) barred this scaling.
  - The 2024 master circular supersedes that circular (Annexure-6, entry 19). Neither the master circular nor the 2024 Insurance Products Regulations restates the bar.
  - The claim still goes to review: the bar's reason, that an ICU has no categories to choose from, still holds, and the practice is contested.
  - Para 6's per-expense test is recorded as not modelled.
- [x] YAML through `go.yaml.in/yaml/v3`, which becomes a direct dependency in session 1.
- [x] Golden cases. Each case's figures are independently checked: recomputed from the case's facts, without the engine, plus your own spot checks. The checked figures are committed as `evals/engine/golden-cases.csv`. A test compares the CSV with the case files: a case with no CSV rows is unchecked, and any mismatch fails. The runner reports each case as pass, fail or unchecked, and the gate needs 40 cases checked and passing. CLAUDE.md forbids changing the expected values or the CSV to make a test pass, and forbids skipping or weakening the tests that compare them. The CSV columns hold only IDs and paise:

  | Column | Holds |
  | --- | --- |
  | `case_id` | The case file's name without `.yaml` |
  | `kind` | `line_payable`, `adjustment`, `claim_payable` or `flag` |
  | `line_id` | The line, for `line_payable`, `adjustment` and a flag about one line; empty otherwise |
  | `code` | The rule ID for an `adjustment`, or the reason code for a `flag`; empty otherwise |
  | `paise` | A non-negative integer: the line's payable, the amount deducted, the claim's payable, or the flag's figure (empty when the flag has none) |

  The CSV must hold exactly these rows:
  - Each case has one `claim_payable` row, and one `line_payable` row for every line, including a line that pays zero.
  - Each adjustment with a non-zero amount has one row. Zero-amount adjustments aren't emitted, so they have no rows.
  - A flag about one line has one row carrying that line's ID. A claim-level flag has at most one row per claim, with `line_id` empty.
  - `(case_id, kind, line_id, code)` is unique.

  The CSV test sorts both sides before comparing. It accepts a UTF-8 byte-order mark and CRLF line endings, because Windows tools often write both, and it fails on rows for a case that doesn't exist. It also checks the identity from session 2 on your rows: for each line, its billed amount in the case file, less its `line_payable` row, equals the sum of its `adjustment` rows.
- [x] Scope. Pre- and post-hospitalisation expenses stay out, as a v1 non-goal in architecture.md, and the engine flags any line dated outside the stay. A same-day stay (day care) is valid input with one day, and gets a cataract day-care golden case. AYUSH has no limit of its own in this pack; whether the hospital qualifies is a fact for M3.

## Session 1: the rule pack

- [x] `rulepacks/arogya-sanjeevani/godigit-2024.yaml` holds the pack ID, its version, the wording's UIN (GODHLIP24179V022324) and the wording's validity dates. It also holds every limit in paise or basis points, each citing its clause ID and page. Rule IDs, like clause IDs, never change once published.
  - The wording states no validity dates, and no source gives this version's start date. `valid_from` is 2023-04-01, a lower bound: the UIN's suffix 2324 is the financial year this version was filed, so it can't apply before 1 April 2023. A pack comment records this.
- [x] Confirm the page numbers of the passages this plan and ADRs 0009 and 0010 quote: E.II.28(c), Definition 10 and Definition 26 in the wording (Definition 26 is on page 4 of 25 in your copy), and para 7 of the 2020 circular. The pack cites pages.
- [x] Each line category is listed exactly once, under one of three treatments:
  - Capped per day, in a bucket. The room bucket takes room rent, boarding and nursing, which C.I.1 (i) caps together, so separate nursing and patient-diet lines count toward its daily cap. ICU charges form the ICU bucket.
  - Scaled by proportionate deduction.
  - Exempt: pharmacy and consumables, implants and devices, and diagnostics (Note 2), plus road ambulance. A comment in the pack says why ambulance is exempt: it has its own limit, and Note 2 scales only expenses "incurred at the Hospital".
- [x] The room ratio. Note 2 doesn't say whether the ratio uses the bucket's daily total or the room rent alone. The pack uses the bucket's daily total, because that is what the cap measures. A comment in the pack records this, and case 04 pins it. Session 3 settled how the ratio counts days (step 3 below).
- [x] Annexure A Lists I to V, as list IDs with their effect: not payable, or folded into room, procedure or treatment charges. The item text gets transcribed in M3, when code-side item matching needs it.
- [x] The order of operations, as a list of step names:
  1. check eligibility;
  2. drop listed items;
  3. set aside unmodelled lines;
  4. apply daily caps;
  5. apply proportionate deduction;
  6. apply sub-limits;
  7. cap at the cover left;
  8. take the co-payment (ADR 0009);
  9. take the bill discount.

  Eligibility is a pass-through until session 5, but it is declared now so the step checks cover it. This plan numbers the six wording steps (2 and 4 to 8 in the list) as steps 1 to 6, as architecture.md does. Eligibility, setting aside unmodelled lines and the discount have no wording step number. The engine runs the order the pack declares, so changing the order means a new pack version.
- [x] A strict parser in `internal/rules`. It takes bytes, so it stays pure, and it decodes through `yaml.Node` so each check sees the source text. Each of these is an error that names the field:
  - an unknown field;
  - a float;
  - an integer that doesn't match `^(0|[1-9][0-9]*)$`. A lone 0 is valid; a sign, a leading zero, `0x`, `0o` or an underscore is not;
  - a second document, an anchor, an alias or a merge key;
  - a rule that cites an undefined clause, or cites none without being marked unmodelled (session 3);
  - a category listed twice or not at all;
  - a step that is missing or repeated.
- [x] A small `rulepacks` package embeds the YAML with `go:embed`, because `internal/rules` can't import `io/fs`. A pack's identity is its ID, its version and the SHA-256 of its bytes. Every result carries that identity, so a replay can prove it used the same pack. A published pack never changes; from the M1 gate onward, a test pins each published version's SHA-256 (session 7).
- [x] Pack selection goes through the policy period, because architecture.md picks the pack by policy version. The period that contains the admission date names its wording's UIN, and the newest published pack for that UIN applies; M3 pins it on the claim. The pack's validity dates are checked against the period's start date. Selecting by admission date alone would apply a revised wording to a policy that hasn't renewed onto it. These are errors: no pack for the UIN, a period that starts outside the pack's validity, and two packs with the same UIN and version.

Done when the pack loads and validates, each malformed variant in a table test fails with an error naming its field, a fuzz test on the parser finds no panic, the selection tests pass (including a period that started before a revised wording), and `go run ./cmd/dev check` passes.

## Session 2: engine types, money and the golden-case format

Three fixes from the review of session 1 come first. The pack is unpublished, so its version stays 1.

- [x] ICU days can overpay. Definition 24 counts critical-care nursing and intensivist charges as ICU charges, but the pack put every nursing line in the room bucket, so an ICU day billed ₹12,000 for the ICU, ₹2,000 for an intensivist and ₹1,000 for nursing paid about ₹12,667 instead of ₹10,000. A bucket line dated in a segment of the other kind (nursing or patient diet on an ICU day, or ICU charges on a room day) now raises `bucket_segment_mismatch` and is held to both caps: its own bucket's first, then the segment's bucket, which it joins. Putting such a line only in the segment's bucket can pay more than the other reading: ₹8,000 of nursing on an ICU day with no ICU charge would pay ₹8,000 under the ICU cap but ₹5,000 under the room cap. Both caps together are at or below either reading, and a person decides. Session 3 codes it, and the example becomes a golden case.
- [x] A later pack can drop ICU scaling (ADR 0010). A proportionate-deduction rule now has an `effect`, `scale` or `no-scaling`. A no-scaling rule still covers its target and cites its reason (for ICU, para 7 of the 2020 circular), so every step keeps covering room and ICU and dropping ICU scaling changes only the pack. A parser test parses that variant; the engine side lands in session 3.
- [x] `valid_from` is 2023-04-01, the start of the financial year in the UIN's suffix, as a lower bound. The real start date is unknown.

- [x] Money gains a checked `Add`, `Sum` and `Sub`, which return an error on overflow or a negative result. It also gains `MulDiv` for ratios such as 5,000 ÷ 8,000, half-up in 128 bits, so a ratio is never rounded to basis points first. A zero divisor is an error, never a panic. Each function gets table tests and a fuzz test against `math/big`, like `Percent` and `Allocate`.
- [x] A civil `Date` type with no time zone. It counts days and adds months, with one documented rule for month ends: 31 January plus one month is 28 or 29 February. Tests cover leap years.
- [x] Input and output types for `Adjudicate(pack, facts, cover)`.
  - Facts:
    - the stay: admission, discharge and stay segments;
    - whether the hospital uses differential billing: yes, no or unknown;
    - a bill-level discount, in paise and at least zero;
    - the lines: ID, category, service date, quantity, unit price, amount, item-list match, package marker and treatment details;
    - for session 5: the condition's waiting-period group, whether it was an accident, and its onset date when known.
  - Cover, for the policy year:
    - the sum insured as tranches, each with the date its continuous cover began (portability included);
    - the cumulative bonus granted for the year;
    - the total paid so far this policy year;
    - cataract use per eye;
    - C.I.6 use.

    Until session 5, every tranche counts.
  - Result: each line's billed and payable amounts, the adjustments (line ID, rule ID, clause ID, inputs and amount), the flags, the totals, the pack's identity, and the claim's ledger effects. Every reduction is an adjustment, including a line's share of a claim-level reduction, so for every line, billed less payable equals the sum of its adjustments.
- [x] The ledger effects use the same terms as the cover, so the next claim reads back what this one wrote:
  - The sum insured and bonus consumed equal the payable (Definition 10).
  - Cataract use per eye, and C.I.6 use, are what those lines were admitted at before the co-payment. The sub-limit applies before the co-payment, so counting the paid amount would let an eye's total pass its limit.
- [x] Flags, as one closed list of reason codes. A flag about one line carries its line ID, and a claim-level flag appears at most once per claim.

  | Code | About | Raised when | Figure |
  | --- | --- | --- | --- |
  | `room_days_mismatch` | Line | The days a category's bucket lines cover together in a segment are neither its calendar days nor one fewer; raised on each of those lines | None |
  | `bucket_segment_mismatch` | Line | A bucket line is dated in a segment of the other kind: nursing or patient diet on an ICU day, or ICU charges on a room day | None |
  | `undated_line` | Line | A line has no date in a stay with more than one rate | None |
  | `outside_stay` | Line | The unmodelled-lines step deducts a line dated outside the stay | None |
  | `package_rate` | Line | The unmodelled-lines step deducts a line billed at a package rate | None |
  | `bill_discount` | Claim | The bill carries a discount | The discount |
  | `differential_billing_unknown` | Claim | The facts don't say whether the hospital uses differential billing | None |
  | `no_room_charge` | Claim | A segment has no room or ICU charge, so it has no ratio | None |
  | `icu_ratio_scaling` | Claim | Lines are scaled by an ICU ratio | The total ICU-ratio deduction |
  | `cover_exceeded` | Claim | The admissible total exceeds the cover left | The co-pay-first payable, after the same discount |
  | `tranche_excluded` | Claim | A sum-insured tranche is still inside a waiting period | The total excluded sum insured |
  | `proposed_denial` | Claim | Every tranche is excluded; or the payable is zero, no line was zeroed as unmodelled (`package_rate`, `outside_stay`), and the discount didn't take it to zero | None |
- [x] Input checks:
  - Line IDs are unique.
  - Amounts and the discount are at least zero.
  - Quantity × unit price equals the amount.
  - Stay segments are inclusive date ranges that tile the stay from admission to discharge, so each date falls in exactly one segment. A transfer day belongs to the new room, and a same-day stay is one segment of one day.

  Bad input is an error; a flag is for valid input that the engine can't decide.
- [x] Determinism: the result path never ranges over a map. It uses slices in a fixed order, or sorts a map's keys first.
- [x] The golden-case format, in `evals/engine/golden/`: one YAML file per case, holding the facts, the expected result, and the worked arithmetic as comments. The facts half has the shape of a scenario spec, which M2's generator will reuse; that is the plan's answer to the risk of the generator running late. Every case is synthetic (ADR 0006).
- [x] A test outside `internal/rules`, which can't read files, embeds every case and runs it. On a failure it prints a diff per line and per adjustment. A second test compares `golden-cases.csv` with the expected figures in the case files, following the row rules and the identity check above.
  - Until every step is coded, the runner compares a figure only once the engine codes its step and every step before it in the pack (`rules.Implemented`). Payables and the ledger need every step. Eligibility counts as coded for a case that expects no eligibility adjustment and no `tranche_excluded`, because until session 5 it passes every tranche, which is what such a case expects. A checked case fails the test when a compared figure differs; otherwise it is reported as pending, with every figure that differs. Once session 5 codes eligibility, every figure is compared.
  - Each case file is also checked against itself: one payable per line, billed less payable equal to the line's adjustments, lines adding up to the claim, and every rule and flag known.
- [x] I write the first 10 cases, starting with the plan's worked example, and you check them and send their CSV rows before any step is coded.
  - Written, with their arithmetic in comments, in `evals/engine/golden/`. Their checked figures are in `golden-cases.csv`, and the CSV test matches all 10.

Done when money's new functions pass their table and fuzz tests, the input-check table tests pass, the CSV test matches your rows for every checked case, and the golden runner loads all 10 cases and reports each one failing against the stub engine.

## Session 3: item lists, unmodelled lines, daily caps and proportionate deduction (steps 1 to 3)

- [x] Step 1: a line matched to an Annexure A list pays nothing, with an adjustment that cites that list.
- [x] Setting aside unmodelled lines, a declared step of its own, after dropping listed items and before the daily caps. Lines the engine can't model pay nothing for now, which is at or below every reading of them, and a reviewer decides.
  - The step deducts what is left of each package-rate line, and of each line dated outside the stay, by one adjustment under its own rule ID, `unmodelled-package-rate` or `unmodelled-outside-stay`.
  - It raises `package_rate` or `outside_stay` only for a line it actually deducts. A line an Annexure list already zeroed keeps the list's adjustment as its reason, and raises neither flag.
  - Because the step runs before the daily caps, a set-aside line adds nothing to a bucket's total or a segment's ratio.
  - Neither rule rests on a clause of the wording, so their clause field is empty. The pack marks both rules unmodelled, which is the only way a rule may cite no clause, and a comment points to architecture.md's review triggers.
- [x] Step 2: each bucket is capped per day, at the lower of a share of the eligible sum insured and a rupee ceiling: 2% or ₹5,000 for the room bucket, and 5% or ₹10,000 for ICU.
  - The daily cap is multiplied by the smaller of the days billed and the segment's calendar days, so billing extra days never raises the allowance. Each line covers its quantity of days from its service date, so a bucket's days billed are the days its lines cover together (session 4 fixed this; see there).
  - The days billed may equal the segment's calendar days or be one fewer, because hospitals often skip the discharge day. This is judged per category, on the days that category's lines cover together. Anything else raises `room_days_mismatch` on each of that category's lines, including a lump-sum line billed as one unit for a longer stay.
  - A bucket line's service date is its first billed day, and that picks its segment. So in a stay with more than one segment, an undated bucket line is an input error naming the line; in a stay with one segment, it belongs to that one.
  - The bucket's reduction is spread across its lines with `Allocate`, sorted by ID, in the same way as the claim-level reductions.
  - A bucket line dated in a segment of the other kind raises `bucket_segment_mismatch` and is held to both caps. First its own bucket's cap applies, under that bucket's rule. Then it joins the segment's bucket at what that cap left, and the segment's cap applies under the segment's rule. Per day that pays min(segment lines + min(line, own cap), segment cap). Its billed amount counts in the segment's ratio.
- [x] Step 3: each scaled line is multiplied by its segment's ratio in one `MulDiv`, so it's rounded once.
  - The ratio follows Note 2's wording, "the admissible rate per day bears to the actual rate per day", at most 1. It counts the days billed: the cap × the days billed ÷ the bucket's billed total.
  - When the days billed are at most the segment's calendar days, that is the capped bucket total over the billed bucket total. When more days are billed, the allowance (step 2) still counts only the segment's calendar days, but the ratio doesn't, so an extra billed day lowers the bucket's payable and not the other lines'. `room_days_mismatch` still sends the claim to a person. Case 22 pins this.
  - A line held to both caps (`bucket_segment_mismatch`) is held to both in the ratio too: the admissible side is min(segment lines + min(line, own cap × its days billed), segment cap × days billed), over the segment's billed total, which includes the line's billed amount. Without such a line, that is the cap × the days billed ÷ the billed total.
  - The room ratio and the ICU ratio are separate rules, and the ICU rule raises `icu_ratio_scaling` (ADR 0010). ICU charge lines are capped but never scaled.
  - A rule whose effect is `no-scaling` skips scaling in segments of its kind, deducts nothing and raises nothing. A test runs a pack variant whose ICU rule is `no-scaling`.
  - A segment with no room or ICU charge has no ratio: scaling is skipped there, and the engine raises `no_room_charge`.
  - For a hospital without differential billing, the step is skipped (Note 3), so it raises neither `no_room_charge` nor `undated_line`: no ratio is needed. When the facts don't say, the step follows the wording and raises `differential_billing_unknown`.
- [x] An undated line in a stay with more than one rate gets the lowest ratio in the stay and raises `undated_line`, so its interim figure is never above any reading of the bill. A reviewer supplies the date.
  - Only a scaled line raises it. An exempt line's figure doesn't depend on its date, and a bucket line needs a date (step 2).
- [x] Golden cases:
  - the worked example up to its ₹1,22,500 subtotal;
  - one sum insured where 2% binds and one where ₹5,000 binds;
  - a room rate just over the cap and one just under it;
  - a separate nursing line that pushes the room bucket over its cap, which pins the ratio choice;
  - a room billed one day short (accepted), a lump-sum room line (flagged), and a hospital billing more days than the segment has (the cap uses the segment's days);
  - a move from ward to ICU, with the transfer day and the ICU-ratio flag;
  - ICU days billed ₹12,000 for the ICU, ₹2,000 for an intensivist (classed `icu-charges`) and ₹1,000 for nursing, which pay ₹10,000 a day and raise `bucket_segment_mismatch` on the nursing line;
  - a segment with no room charge;
  - an undated line;
  - a package-rate line;
  - differential billing answered no, and answered unknown;
  - one case for each Annexure list;
  - more days billed than the segment has, with a scaled line, which pins the per-day ratio;
  - a line dated outside the stay;
  - a package line an Annexure list has already zeroed, which keeps the list's reason and raises no `package_rate`.

  Written as cases 11 to 24 in `evals/engine/golden/`, with their arithmetic in comments. Cases 01 to 06 already cover the rest: the worked example (01), 2% binding (02) and ₹5,000 binding (01), a rate just over the cap (03), the nursing line (04), a room billed one day short (01 to 04), the ICU example (05), and differential billing answered no (06) and unknown (03). All 24 are checked and pass.

Done when every golden case for steps 1 to 3 is checked and passes, including the worked example's ₹1,22,500 subtotal, to the paisa.

## Session 4: sub-limits, the cap, co-payment and the discount (steps 4 to 6)

- [x] Fix how a bucket's days billed are counted. Session 3 counted the most days on any one line, so a bill with one room line per day, which is common, counted as 1 day. Four daily ₹4,000 lines with a ₹50,000 surgeon's fee paid ₹19,593.75 with four `room_days_mismatch` flags instead of ₹62,700 with none.
  - Each line covers its quantity of days from its service date; an undated bucket line, possible only in a stay with one segment, covers them from the segment's first day. A bucket's days billed are the days its lines cover together, for the allowance (step 2) and the ratio (step 3) alike.
  - `room_days_mismatch` is judged per category, on the days that category's lines cover together, and raised on each of them.
  - The fix changes none of cases 01 to 24. Case 25 is that bill.
- [x] Step 4 applies three sub-limits to the lines the facts tie to each treatment. Percentage limits use the eligible sum insured, without the cumulative bonus.
  - Cataract: the lower of 25% of the sum insured and ₹40,000 per eye, less what that eye has already used this policy year.
  - Listed modern treatments (C.I.6): 50% of the sum insured, less what has already been used.
  - Road ambulance: ₹2,000 per hospitalisation.
- [x] Step 5 caps the admissible total at the cover left: the eligible sum insured plus the cumulative bonus granted for the year, less the total paid this policy year, and never below zero.
  - When the admissible total exceeds the cover left, the engine raises `cover_exceeded`, carrying the co-pay-first payable after the same discount, so the two figures compare like for like (ADR 0009).
  - That figure is never lower than the payable, and can equal it: when no cover is left, or when rounding closes the gap. For example, 29 paise of cover against 30 paise admissible pays 28 paise either way.
- [x] Step 6: a co-payment of 5% of the capped amount, computed once with `Percent`.
- [x] The bill discount, declared last, deducts the smaller of the discount and the payable, spreads it across the lines with `Allocate`, and raises `bill_discount`. No line goes below zero, because the deduction never exceeds the lines' total. Deducting it after the co-payment is at or below every reading of where the discount fell, and a reviewer decides. Its rule cites Definition 26, "Medical Expenses", which covers only what was "necessarily and actually incurred"; a discount was never incurred.
- [x] Every claim-level reduction is spread across the lines with `Allocate`, weighted by each line's amount so far. The lines are sorted by ID first, so the input order never moves a paisa. `Allocate` returns `ErrNoWeight` whenever every weight is zero, even for a zero total, so the engine skips allocation when there is nothing to spread.
- [x] The ledger effects are filled in: the payable is the sum insured and bonus consumed, and the cataract and C.I.6 lines' amounts before the co-payment are their sub-limit use.
- [x] `proposed_denial` means the policy makes the claim pay nothing. The engine raises it when every tranche is excluded (session 5), or when the payable is zero, no line was zeroed as unmodelled, and the discount didn't take it to zero.
  - A claim that pays nothing only because a line is billed at a package rate or dated outside the stay isn't a proposed denial. Calling it one would steer a reviewer toward denying a claim the engine couldn't read. Its `package_rate` or `outside_stay` flag sends it to review instead.
  - A proposed denial is a valid result, not an error, and a person decides (rule 6).
- [ ] Golden cases:
  - the worked example through to ₹1,16,375;
  - an admissible amount just over the cover left (flagged, with the co-payment taken on the cover left) and one just under it (no flag);
  - a claim for both eyes, and a second claim for an eye already treated, whose cover input is the first claim's ledger effects;
  - a cataract done as day care, in a same-day stay;
  - an ambulance bill over ₹2,000;
  - a modern treatment over its limit;
  - a claim where 5% taken line by line would round differently from 5% of the total;
  - a bill-level discount;
  - a claim where every line is non-payable, which is a proposed denial, not an error;
  - a package-billed cataract claim whose only payable line is the package line, which pays zero and raises `package_rate`, not `proposed_denial`.

  Cases 01 to 24 already cover the worked example (01), the cataract as day care (08), the ambulance (06), the rounding (10) and the discount (09); case 07 exceeds the cover by ₹2,000. The rest are written as cases 26 to 32, with case 25 for the days-billed fix, and wait for their check: 26 is a paisa over the cover left and 27 a paisa under, 28 and 29 are the two cataract claims, 30 is the modern treatment, 31 the claim where every line is non-payable, and 32 the package-billed cataract.

Done when every golden case written so far is checked and passes, and the worked example matches the plan's table line by line.

## Session 5: eligibility and waiting periods

- [ ] The eligibility step, declared first since session 1, stops being a pass-through. Every percentage limit and the cap use the eligible sum insured.
- [ ] The waiting periods:
  - 30 days at the start (Excl03). It doesn't apply to accidents, and Excl03(ii) drops it once cover has run continuously for more than 12 months.
  - 24 or 36 months for listed conditions (Excl02).
  - 36 months for pre-existing disease (Excl01). Where a listed condition is also pre-existing, the longer period applies (Excl02(c)).

  The facts say which group the condition falls into; deciding that is the agent's job in M3.
- [ ] Each tranche is tested on its own. An increase in the sum insured restarts the waiting periods for the added amount (Excl01(b), Excl02(b), Excl03(iii)), so only the tranches past the relevant period count toward the eligible sum insured. A tranche that is left out raises `tranche_excluded`, carrying the total sum insured excluded.
- [ ] Excl03 excludes "treatment of any illness within 30 days".
  - When the facts give an onset date inside the first 30 days, the claim gets a proposed denial and its flag. That follows the lower reading, by the plan's rule that an interim figure is never above any reading.
  - The admission date decides only when no onset date is known.

  A comment in the pack records this.
- [ ] When no tranche is eligible, the claim gets a proposed denial: payable zero, each line's billed amount deducted by an adjustment citing Excl01, Excl02 or Excl03, and `proposed_denial`. The engine never denies.
- [ ] Golden cases:
  - the last day inside each period and the first day after it;
  - for Excl03, an admission on day 30 and on day 31 with no onset date, and an onset inside the 30 days with an admission after them (a proposed denial, flagged);
  - an accident in the first 30 days;
  - a claim early in the second year of continuous cover, which has no 30-day wait;
  - an inception on 29 February;
  - a listed condition that is also pre-existing;
  - a sum-insured increase still inside its waiting period, which excludes the tranche, raises the flag and uses the lower sum insured for the percentage limits.

Done when every boundary case is checked and passes on both sides, and every proposed denial and every excluded tranche carries its flag.

## Session 6: properties, fuzzing and explanations

- [ ] Property tests over generated claims, using native fuzzing so no dependency is added:
  - No line pays more than it billed or less than zero.
  - The lines add up to the totals.
  - For every line, billed less payable equals the sum of its adjustments.
  - Raising the sum insured never lowers the payable.
  - Shuffling the lines changes nothing.
  - The same input run twice gives identical results.
  - `cover_exceeded`'s figure is never lower than the payable.
- [ ] Fuzz the engine with packs and facts near the `int64` limits. The result must be an error, never a panic or a silent overflow.
- [ ] Adjustments and flags marshal to JSON holding only IDs, reason codes and amounts, ready for the `adjustments` table and audit `details` in M3. A golden JSON file pins the shape.
- [ ] An `EngineVersion` constant joins the pack's identity on every result.

Done when the property tests pass, each fuzzer runs clean for 10 minutes locally (CI runs their seed corpora), and `go run ./cmd/dev check` passes.

## Session 7: the CLI, 40 cases and the gate

- [ ] `cmd/evalrunner` gets two commands. `engine` reports each golden case as pass, fail or unchecked, with the diff for each failure. `explain <case.yaml>` prints one claim's lines and adjustments laid out like the plan's worked example. `go run ./cmd/dev golden` runs `engine`.
- [ ] Bring the golden set to 40 checked cases. Every ambiguity met this week has an ADR or a comment in the pack, and a case that pins it.
- [ ] Publish the pack: a test pins its SHA-256, and from here on any change to it means a new version.
- [ ] Update the status in CLAUDE.md.
- [ ] Gate: from a fresh clone, `go run ./cmd/dev golden` reports 40 cases checked and passing, and `go run ./cmd/dev check` passes. There's no recording: paste the `explain` output for the worked example into the PR.

Done when all 40 golden cases are checked and pass, which is M1's exit gate.

## Not this week

- The tables for rule packs, line items, stay segments, adjudications, adjustments and policy balances arrive in M2 and M3, when something writes to them. Pinning a pack on a claim, and locking the balance ledger at finalize, arrive with them.
- `policy_periods` needs a wording column holding the UIN, for pack selection to read. It already holds the sum insured per period, so M3 can build the tranches from it.
- M3 takes the cover and the pack from the policy period that contains the admission date, even when the stay runs past renewal.
- The workflow doesn't call the engine until M3. That is also where the review-routing thresholds get set: a billed or payable amount above a set value, or a payable near a limit.
- Code-side item matching against the Annexure A text waits for M3, and rendering synthetic packets from scenario specs waits for M2.
- M3's extraction must class intensivist and critical-care nursing charges as `icu-charges` (Definition 24), because the engine can't tell them from other doctors' fees or ward nursing.
- Differential billing judged per expense (para 6 of the 2020 circular), and pre- and post-hospitalisation expenses, are not modelled.

## Done by the end of week 2

- One rule pack encodes the Arogya Sanjeevani wording, every limit in it cites its clause, and its SHA-256 is pinned.
- `internal/rules` computes the payable and explains it with one adjustment per rule. It stays pure and gives the same answer every time.
- 40 golden cases, independently checked, pass in CI and through `go run ./cmd/dev golden`, and property and fuzz tests cover the rest.

Next is M2, intake and extraction. The build plan is in [architecture.md](architecture.md#repo-structure-and-build-plan).

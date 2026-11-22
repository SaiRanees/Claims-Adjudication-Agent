# 0011. Take a segment cap's cut from the lines held to both caps first

- Status: Accepted
- Date: 2026-11-22

## Context

A bucket line dated in a segment of the other kind (nursing or patient diet on an ICU day, or ICU charges on a room day) raises `bucket_segment_mismatch` and is held to both daily caps (week 2, session 2). First its own bucket's cap applies. Then it joins the segment's bucket at what that cap left, and the segment's cap applies to the bucket as a whole.

Until now the segment cap's cut was spread across every line in the bucket by amount, the same way as in any other bucket. Its totals are right, but its split between lines breaks a property the engine should keep: raising the sum insured never lowers the payable.

A higher sum insured can raise the line's own cap, so more of the line enters the segment's bucket. If the segment's cap is already at its rupee ceiling, the cut grows by the same amount, and a pro-rata split moves part of it from the segment's own lines onto the line held to both caps. When that line is tied to a sub-limit that binds, the sub-limit then takes off what the line gained, and the payable falls.

Take an ICU day billed ₹12,000 for the ICU and ₹6,000 for nursing, with the nursing tied to a cataract claim whose ₹40,000 lens implant already reaches the ₹40,000 limit:

| | Sum insured ₹2,00,000 | Sum insured ₹2,50,000 |
| --- | --- | --- |
| ICU cap (5%, at most ₹10,000) | ₹10,000 | ₹10,000 |
| Room cap (2%, at most ₹5,000) | ₹4,000 | ₹5,000 |
| Nursing after its own cap | ₹4,000 | ₹5,000 |
| ICU bucket, and its cut | ₹16,000, ₹6,000 | ₹17,000, ₹7,000 |
| Pro rata: ICU line, nursing line | ₹7,500, ₹2,500 | ₹7,058.82, ₹2,941.18 |
| After the cataract limit | ₹47,500 | ₹47,058.82 |

The fuzzer found such a claim: raising the sum insured from ₹2,25,000 to ₹3,09,644.33 lowered its payable by ₹195.75. That input is kept as a regression seed in `internal/rules/testdata/fuzz/FuzzAdjudicateProperties/`.

## Decision

A segment's cap takes its cut from the lines held to both caps before the segment's own lines.

- The cut is the bucket's total less the segment's allowance, as before. The lines held to both caps take it first, up to what their own cap left them, spread across them with `Allocate` by amount, in ID order.
- Any cut left over is spread across the segment's own lines with `Allocate` by amount, in ID order, as in any bucket.
- The reason: the ICU limit covers the ICU's own charges first, and the room limit the room's. A line that joined the bucket only because it is held to the lower of two readings gets what is left.
- A line's own cap, the bucket's totals, the ratio for proportionate deduction and the flags don't change. Only the split between lines does.

In the example, the nursing line pays nothing under either sum insured and the ICU line pays ₹10,000, so the admitted total is ₹50,000 under both.

## Consequences

- A higher sum insured no longer moves a cut onto a line held to both caps. The segment's own lines pay the smaller of their total and the segment's allowance, whatever the other line brings in. The lines held to both caps pay the smaller of what their own cap left them and what the allowance leaves after the segment's own lines. Each of those totals is exact to the paisa and grows with the sum insured.
- Rounding can still lower the payable by a few paise. The property allows for exactly that and no more, with a bound worked out per claim (below). This was decided on 22 November. The bound isn't an exception: it checks every claim, it is zero for most of them, and a drop beyond it is still a fault.
- Golden case 05 changes: the nursing line pays nothing and the ICU and intensivist lines pay more. The total stays ₹24,700. No other golden case has a line held to both caps.
- The daily caps' totals never change, but a later step that reads lines one by one can: a sub-limit tied to some of the bucket's lines and not others. Then the payable can differ from the pro-rata split in either direction. The example pays ₹2,500 more than the pro-rata split did. With the ICU line tied instead, it would pay less. Every such claim raises `bucket_segment_mismatch`, so a person reviews it as before.
- The rule is in the engine, not the pack, because it decides how a cut is attributed, not how much is cut. The pack's comment on the daily caps refers here.

### The rounding bound

Without rounding, a higher sum insured never lowers the payable. Every limit and cap is at least as high, each line's share of a cut is proportional to what it pays, the lines held to both caps and the segment's own lines pay as above, and the steps after the sub-limits act on the total. Rounding a split to whole paise can break this, but only through a sub-limit:

1. Largest remainder keeps each line of a split within a paisa of its exact share, and the shares add up to the split's total. So rounding moves less than a paisa per line, and only between the lines of that split. A line held to both caps is split once at each cap, so it can move by less than a paisa at each.
2. Those paise reach the payable only when a later sub-limit holds some of the split's lines and not others. Moving paise between lines that every later sub-limit treats alike leaves the total as it was. Across one sub-limit, the paise moved in or out of it add up to less than the smaller of the split's lines inside it and outside it.
3. After the sub-limits, the cover cap, the co-payment and the discount act on the total. The co-payment is rounded half-up, so it rises by at most a paisa for each paisa the total rises. None of these steps moves the payable by more than the total moves.

So at each sum insured, the payable is less than B paise from the unrounded one. To get B, take each split the claim made (a split that made a cut, so at least one of its lines took a share) and each sub-limit after it, and add the smaller of the split's lines inside that sub-limit and outside it. Lines that entered the split with nothing to pay don't count. There are two kinds of split:

- A daily cap's cut over one segment's bucket: its own lines, and its lines held to both caps, counted once at each cap. Every sub-limit comes after it.
- The cataract and modern-treatment limits' cuts. The road-ambulance limit comes after them, and a road-ambulance line can be tied to a treatment.

The unrounded payable doesn't fall, so the drop is less than B at the lower sum insured plus B at the higher. Paise are whole, so the drop is at most B₁ + B₂ − 1 paise, and nothing when both are zero. Where both are zero, the property holds exactly, as it does for 279 of the property fuzzer's 300 seed claims. A drop beyond the bound is still a fault: on the engine before this decision, the fuzzer's first input lowers the payable by 19,575 paise against a bound of 3. `roundingBound` in `internal/rules/properties_test.go` computes the bound.

Three sources are known. Each lowers the payable by one paisa, and `TestRoundingBound` pins each one:

- **The Alabama paradox.** Largest remainder isn't monotone in its total, so a smaller cut can take more from one line. A room day is billed ₹691.06 for the room (untied), ₹2,954.23 for nursing and ₹2,258.26 for diet. Nursing and diet are tied to a cataract whose ₹40,000 implant reaches the limit. The claim pays 3,844,483 paise at ₹2,00,000 and 3,844,482 at ₹2,00,000.50. Its bound is 1.
- **The double rounding.** Lines held to both caps are split at their own cap and again at the segment's. The fuzzer's input `9d50a174da0c39c0` has two room lines on an ICU day, one of them tied to a cataract. Their total is 383,187 paise at both sums insured, but the tied line's share rises by a paisa, and the payable goes from 4,785,234 paise to 4,785,233. Its bound is 3.
- **A sub-limit's own split.** The cataract limit's cut is spread across a road-ambulance line and other lines. It can leave the ambulance line a paisa more as the sum insured rises, and the ambulance limit takes it off. The payable goes from 3,629,282 paise to 3,629,281. Its bound is 1.

## Alternatives considered

- **Narrow the property, and skip claims with a line held to both caps and a binding sub-limit.** Rejected: a property with an exception for the very claims that break it proves nothing about them, and a higher sum insured that pays less is a result no reviewer could explain.
- **Keep the pro-rata split.** Rejected: it lets the sum insured lower the payable, and it spends the ICU limit on a line that joined the ICU bucket only under the lower-reading rule.
- **Make every split monotone in its total, so the property needs no rounding bound.** Rejected: it would replace largest remainder, which keeps every line within a paisa of its exact share, in the daily caps and the sub-limits. It would also change how the golden cases split their cuts, and it would still leave the double rounding to remove. A drop of a few paise can be explained as rounding, and the bound still catches anything larger.

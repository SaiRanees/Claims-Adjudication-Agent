# 0012. Apply the road-ambulance limit before the treatment limits

- Status: Accepted
- Date: 2026-11-24

## Context

Step 4, `apply-sub-limits`, applies its rules in the pack's order. Until now that was the cataract limit (C.I.3), the modern-treatment limit (C.I.6), then the road-ambulance limit (C.I.1.1(v)). The wording doesn't say which comes first.

A road-ambulance line can be tied to a treatment: the facts tie it to a cataract or a modern treatment when the ambulance was part of that treatment's cost. In the old order, such a line takes a share of the treatment limit's cut, and then the ₹2,000 ambulance limit cuts it again. Part of the treatment's cut lands on an amount the ambulance limit removes anyway, so the treatment's lines are admitted below the treatment limit, even when what's admissible after the ambulance limit is above it. That is a fault in the rule, not rounding.

ADR 0011 recorded the third rounding case in `TestRoundingBound` as rounding, because the payable fell by a paisa when the sum insured rose. The same claim shows the fault. At a sum insured of ₹2,00,000, the left eye's cataract has a ₹40,000 limit, and three lines are tied to it: a room day capped at ₹4,000, a ₹43,733.03 implant and a ₹5,006.33 road ambulance.

| | Cataract limit first (before) | Ambulance limit first |
| --- | --- | --- |
| Tied lines entering the cataract limit | ₹52,739.36 | ₹4,000 + ₹43,733.03 + ₹2,000 = ₹49,733.03 |
| Cataract cut | ₹12,739.36, of which ₹1,209.30 on the ambulance | ₹9,733.03 |
| Ambulance limit's cut | ₹3,797.03 − ₹2,000 = ₹1,797.03 | ₹5,006.33 − ₹2,000 = ₹3,006.33 |
| Admitted | ₹38,202.97 | ₹40,000 |
| Paid, after the 5% co-payment | ₹36,292.82 | ₹38,000 |

The independent checker for the golden cases has applied the ambulance limit first all along. The two never disagreed, because no golden case tied an ambulance line to a treatment: case 06 is the only case with an ambulance line, and it isn't tied.

## Decision

The road-ambulance limit comes first in `apply-sub-limits`, before the cataract and modern-treatment limits.

- The ambulance limit is that line's own limit, and a treatment limit caps what is left of the lines tied to it. That is the principle of ADR 0011, where a line held to both daily caps meets its own cap first and the shared cap takes what is left.
- Where the wording says nothing about the order, it is also the reading kinder to the insured. A treatment's lines are admitted at the lower of the treatment's limit left and what those lines come to after the ambulance limit. The old order could admit less than both.
- The order is pack data. The pack lists `sub-limit-road-ambulance` first, with a comment citing this ADR, and the parser rejects a pack that puts the cataract or modern-treatment limit before it, with an error naming ADR 0012.
- Whether an ambulance line is tied to a treatment stays the facts' call. M3's extraction decides it.

## Consequences

- No golden figure changes. Case 06's ambulance line isn't tied to a treatment, so the order doesn't touch it.
- Case 52 pins the order: a left-eye cataract as day care, with ₹16,000 of professional fees, a ₹14,000 lens and a ₹3,000 road ambulance, all tied to the cataract, against a ₹25,000 limit. It admits ₹25,000 and pays ₹23,750. The old order admitted ₹24,727.27 and paid ₹23,490.91.
- `EngineVersion` stays 0.1.0. The engine already runs the rules in the pack's order, so its code doesn't change; the figures move because the pack changed, and the pack's SHA-256 records that. The pack was unpublished when this was decided, so it stays version 1, and the M1 gate publishes it with this order.
- The third rounding source in ADR 0011 is gone. With the ambulance limit first, its split works on amounts the sum insured can't move: a road-ambulance line is in no bucket and is never scaled, and the limit is a fixed ceiling. The cataract and modern-treatment limits share no line, so neither's split can be told apart by the other. No sub-limit's split can lower the payable as the sum insured rises any more. `TestRoundingBound`'s third case now pays ₹38,000 at both sums insured, with a bound of 0.
- The daily caps' splits can still lower the payable by a paisa through a sub-limit (ADR 0011's first two sources). `roundingBound` keeps its general count: it reads the sub-limits' order from the pack, still counts a sub-limit's split ahead of a later sub-limit, and stays a sound bound for any pack the parser accepts.

## Alternatives considered

- **Keep the old order.** Rejected: it admits less than the treatment limit when what's admissible after the ambulance limit is above it, by spending part of the treatment's cut on an amount the ambulance limit removes anyway.
- **Never tie a road-ambulance line to a treatment.** Rejected: whether the ambulance was part of a treatment's cost is a fact about the claim, which extraction decides in M3. The engine applies each limit to the lines the facts tie to it.
- **Keep the old order and flag claims that tie an ambulance line to a treatment.** Rejected: the flag would send the claim to a person, but its interim figure would still be below what both limits allow, and the fix is one change to the pack's order.

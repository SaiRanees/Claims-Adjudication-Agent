# 0009. Cap at the cover left before taking the co-payment

- Status: Accepted
- Date: 2026-11-18

## Context

After the line rules, the engine makes two claim-level reductions: the 5% co-payment (E.II.28(c)), and the cap at the sum insured plus the cumulative bonus left for the policy year. The wording doesn't set their order in so many words, and the first draft of the architecture put the co-payment first. The two orders give the same answer unless the admissible amount exceeds the cover left. When it does, they differ, and someone has to pick.

The wording points one way:

- E.II.28(c) applies the 5% "to claim amount admissible and payable as per the terms and conditions of the Policy". An amount above the cover left isn't payable under the policy, so the co-payment applies after the cap.
- The operative clause makes the sum insured plus the cumulative bonus the insurer's maximum liability.
- Definition 10: "A co-payment does not reduce the Sum Insured."

## Decision

Cap first, then take the co-payment.

- Step 5 caps the admissible total at the cover left: the eligible sum insured plus the cumulative bonus granted for the year, less the total paid this policy year, and never below zero.
- Step 6 takes 5% of the capped amount, computed once for the claim with `money.Percent`, and spreads it across the lines with `money.Allocate`.
- The ledger debits only what is paid, never the co-payment (Definition 10).
- The order matters only for a claim whose admissible total exceeds the cover left. Every such claim, and no other, raises a `cover_exceeded` flag that carries the payable the co-pay-first order would give, so a reviewer sees both figures. That figure is never lower than the payable, and can equal it: when no cover is left, or when rounding closes the gap. For example, 29 paise of cover against 30 paise admissible pays 28 paise either way.
- The rule pack lists its steps in this order. Two golden cases pin it: one with an admissible amount just over the cover left, and one just under it.

## Consequences

- The rejected order never pays less, and pays more whenever the two figures differ. Take a cover left of ₹1,00,000 and an admissible total of ₹1,02,000. Cap first: ₹1,00,000 less 5% is ₹95,000. Co-payment first: ₹1,02,000 less 5% is ₹96,900, which is under the cap, so it pays ₹96,900, which is ₹1,900 more. Once the admissible total reaches the cover left divided by 0.95, the co-pay-first order pays all of the cover left, while this order pays 95% of it.
- A capped claim never uses up the cover. After the example above, ₹5,000 of cover is left, and a later claim in the same policy year can use it, again less its co-payment.
- The flagged claims are the ones where this reading costs the policyholder money, and each of them goes to a person.
- Changing the order later means a new pack version and a change to the golden cases, never an edit to a published pack.

## Alternatives considered

- **Co-payment first, then the cap,** as in the first draft. This reads "admissible" as the amount before the cap. It pays more when cover runs short, but E.II.28(c) says "admissible and payable", and it is the cap that decides what's payable.
- **Co-payment taken line by line.** This rounds each line separately, so the total can drift by a few paise from 5% of the claim. The engine's rule is to round a claim-level amount once and spread it by largest remainder.

# 0005. Make the rules engine a pure library, with money in paise

- Status: Accepted
- Date: 2026-11-11

## Context

The engine decides how much is payable: proportionate deduction when the room is above the eligible limit, the 5% co-payment on every claim, sub-limits, waiting periods and the sum insured left in the policy year. A wrong amount is a financial and regulatory error, and every decision has to be explained, and reproduced exactly, months later.

## Decision

`internal/rules` is a pure library. It takes the facts of a claim, a rule pack and the policy year's cover and balance, and returns each line item's payable amount with the adjustments behind it, each citing a clause of the wording. It does no I/O, reads no clock, uses no randomness and calls no model. `.golangci.yml` enforces this: depguard blocks I/O packages in `internal/rules`, and forbidigo blocks `time.Now` there.

Money is an `int64` number of paise everywhere: `bigint` in Postgres, `int64` in Go, an integer in the API. Percentages are integer basis points (5% is 500). Rounding is half-up to the paisa, at the points the rule pack defines. Splitting an amount uses the largest-remainder method, so the parts always add up to the whole, and a fuzz test proves it.

## Consequences

- The same inputs always give the same output. Tests are plain tables and fuzz tests, with no mocks.
- A stored decision can be replayed from its stored inputs, and a model can never change a number.
- The workflow gathers every fact before calling the engine, and passes in the dates it needs, such as the date that ends a waiting period.
- `int64` paise reaches about ₹92,000 lakh crore, far beyond any claim. JavaScript clients read values exactly up to 2^53 paise, about ₹90 lakh crore.

## Alternatives considered

- **A decimal library.** Flexible, but slower, easy to mix scales, and it still needs explicit rounding rules.
- **float64.** Binary floating point can't represent most decimal amounts exactly.
- **Letting the model compute amounts.** Not reproducible, and not auditable.

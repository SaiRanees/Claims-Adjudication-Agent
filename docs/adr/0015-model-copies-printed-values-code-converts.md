# 0015. Have the model copy printed values, and convert them in code

- Status: Proposed
- Date: 2026-11-24

## Context

Line-item extraction has to return amounts, quantities and dates, and every rupee the engine works with starts there. Rule 6 says models never produce amounts; the engine does. But the engine can only compute from what extraction hands it, so how a printed figure becomes paise matters as much as the arithmetic after it.

Indian hospital bills print the same figure many ways: `₹1,23,456.00`, `Rs. 123,456`, `INR 123456/-`, or a bare `123456.00`. Dates come as `22/09/2025`, `22-Sep-2025` or `22.09.25`. A model asked for paise or ISO dates has to convert, and conversion is where a model makes quiet mistakes: a digit dropped from Indian grouping, a day read as a month, a subtotal added into a line. A converted number also can't be checked against the page, because the page doesn't print it.

## Decision

Extraction returns what is printed, and code does every conversion.

- The extraction schemas hold amounts, unit prices, quantities and dates as strings, exactly as printed, or null when absent. No amount appears in a schema as a number. Every property is required, nullable where a value can be missing, so a schema has no optional parameters.
- The model never converts a unit, a format or a currency, never adds anything up, and never fills in a value it can't see. Lines are charges only: subtotals, "carried forward" rows and payments are left out, and discounts and totals are copied into the bill's own fields as printed.
- Code parses each value:
  - Amounts accept ₹, `Rs.`, `INR`, a trailing `/-`, and Indian or Western digit grouping. They become `int64` paise, with overflow checked and no floating point (rule 1). A negative or unparseable amount is a parse failure, recorded with the printed text.
  - Dates read day first, in the formats the generator's templates print and their common variants. A two-digit year must land within a year of the admission.
  - Quantities become whole numbers, because the engine takes whole quantities. A fractional quantity is kept as printed and flagged, never rounded.
  - Descriptions are NFC-normalized, stripped of control characters and capped at 200 characters, with the cut recorded.
- Code then checks the parsed values against the document:
  - Grounding: each amount and unit price must equal, as a parsed value, a number in that page's text layer, so `1,23,456.00` on the page matches `123456` from the model.
  - Arithmetic: quantity × unit price equals the amount, exactly.
  - Totals: once a bill's page ranges are stitched in page order, its lines add up to the printed gross, and gross less discount equals net.

The parser and the checks are pure functions with table tests and a fuzz test, like `internal/rules/money`.

## Consequences

- Every figure the engine sees can be traced to a string on a page and to one parser. A wrong conversion is a parser bug, which a test can reproduce, not a model's off day.
- Grounding compares like with like, so it works.
- Grounding is per page. Two lines on one page whose amounts are swapped pass grounding and the totals check together. Week 4 adds row grounding (an amount must sit on the same printed row as its description, using PDFium's text positions), and until then the eval counts such misses as silent errors.
- More code to write and fuzz than asking for numbers would take.
- Nullable strings count toward the API's limit of 16 union-typed parameters per request ([structured outputs](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)). `line_items.v1` needs about 12, and a test counts them.
- The schema says nothing provider-specific, so a second provider's adapter can fill it unchanged.

## Alternatives considered

- **Ask the model for paise and ISO dates.** It moves conversion into the least checkable place, and the result can't be grounded against the page text.
- **Ask for JSON numbers.** A JSON number decodes as a float unless every reader is careful, which breaks rule 1 at the first careless reader, and it still asks the model to convert.
- **Skip the model: read the text layer and match it with patterns.** Bills differ in layout, column order and wording, and telling a charge from a subtotal or a carried-forward row needs a reading of the page that patterns don't give. Code-side patterns still have a place: they scan each page for instruction-like text beside the model.

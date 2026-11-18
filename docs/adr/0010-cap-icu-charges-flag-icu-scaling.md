# 0010. Cap ICU charges without scaling them, and flag scaling by an ICU ratio

- Status: Accepted
- Date: 2026-11-18

## Context

The wording's proportionate deduction is in C.I.1 Notes 2 and 3. Note 2 says that when the room or ICU charge per day is over its limit, the other hospital expenses are reduced in the same proportion. Pharmacy and consumables, implants and devices, and diagnostics are exempt. Note 3 says the deduction doesn't apply in a hospital that doesn't follow differential billing.

IRDAI's circular IRDAI/HLT/REG/CIR/151/06/2020 of 11 June 2020 limited this practice in paragraphs 4 to 7. Paragraph 7 says: "Insurers are not permitted to apply proportionate deduction for 'ICU charges' as different categories of ICU are not there." Paragraph 6 also bars the deduction for expenses that aren't billed by room category.

The 2024 master circular on health insurance business (IRDAI/HLT/CIR/PRO/84/5/2024, 29 May 2024) never mentions proportionate deduction or ICU charges. Its only use of "proportionate" is about refunding premium. In Chapter III, part IV, it says: "This Circular supersedes all the Guidelines/Circulars listed in Annexure-6." Annexure-6 lists the 2020 circular as entry 19: "IRDAI/HLT/REG/CIR/151/06/2020 dated 11.06.2020 Modified Guidelines on Product filing in Health Insurance Business". That check was made on 18 November 2026, against the circular and its separately published annexure (version 2.0). Both were downloaded from the circular's page on irdai.gov.in ([document 4942918](https://irdai.gov.in/document-detail?documentId=4942918)), and their text was extracted mechanically and searched.

The IRDAI (Insurance Products) Regulations, 2024 were also checked on 18 November. Because the official PDF link is dead, the check used the full text in a third-party copy on legitquest.com. The Regulations don't mention proportionate deduction, ICU charges, room rent or co-payment.

So the 2020 circular is verified as superseded, and neither the master circular nor the Regulations restates its bar on ICU deduction. Only a circular issued after May 2024 remains unverified.

The Go Digit wording (GODHLIP24179V022324) still scales other expenses by the ICU rate.

## Decision

- ICU charge lines are capped per day, at the lower of 5% of the eligible sum insured and ₹10,000. No ratio ever scales them.
- Scaling other lines by an ICU segment's ratio follows the wording (Note 2). It has its own rule ID, `proportionate-deduction-icu`, kept separate from `proportionate-deduction-room`. Every claim that triggers it raises one `icu_ratio_scaling` flag, carrying the total ICU-ratio deduction, so a person reviews it.
- The flag isn't called a known conflict. With the 2020 bar superseded and not restated, no current regulation is known to conflict with the wording. The review route stays anyway: the bar's reason, that an ICU has no categories to choose from, still holds, and ICU scaling is contested.
- Note 3 is modelled per hospital. Differential billing is yes, no or unknown. No skips the scaling. Unknown applies the wording and raises `differential_billing_unknown`.
- Paragraph 6's per-expense test, which looks at whether each expense is billed by room category, is recorded as not modelled. The engine doesn't know which charges vary with the room.

## Consequences

- The deductions that rest on the contested reading carry their own rule ID, so the explanation and the audit trail show exactly which they are.
- Every claim with ICU-ratio scaling reaches a person, who applies whichever reading holds at the time.
- Dropping ICU-ratio scaling later means a new pack version without that rule, and golden cases that show the difference.
- Revisit this decision if a later circular restates the bar, or if Go Digit files a revised wording.

## Alternatives considered

- **Follow the 2020 circular, and never scale by an ICU ratio.** Rejected for now: that circular is superseded, nothing current restates it, and the wording on file still scales. The flag keeps a person on every claim this affects.
- **Follow the wording, and raise no flag.** Rejected: the bar's reason still holds, the regulator has barred the practice before, and an unexplained deduction is the complaint this project is built to answer.
- **Scale the ICU charge lines themselves.** Rejected: those lines are already capped per day, so scaling them as well would deduct twice for the same excess.

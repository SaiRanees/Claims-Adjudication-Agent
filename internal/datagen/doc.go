// Package datagen renders synthetic claim packets and their ground truth
// from scenario specs. The same spec and generator version give the same
// bytes on every run and on every OS, so recorded model responses keep
// matching (ADR 0014). Hospitals and people are invented, and every page
// says it's a synthetic test document (ADR 0006).
//
// A packet can hold hospital and pharmacy bills, payment receipts, a claim
// form, a photo ID, a discharge summary, OT notes and implant invoices. The
// photo ID copies no real ID's design or number format.
package datagen

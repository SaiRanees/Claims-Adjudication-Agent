// Package datagen renders synthetic claim packets and their ground truth
// from scenario specs. The same spec and generator version give the same
// bytes on every run and on every OS, so recorded model responses keep
// matching (ADR 0014). Hospitals and people are invented, and every page
// says it's a synthetic test document (ADR 0006).
package datagen

// Package workflow defines the Temporal workflows and activities that take a
// claim from submission to decision.
//
// Workflow code is deterministic: no clock, network, randomness or goroutines
// except through the Temporal SDK. Activities do the I/O, and workflows and
// activities exchange IDs only, never documents or extracted text (ADR-0004).
package workflow

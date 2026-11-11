// Package review manages review tasks: the queue, time-limited leases, the
// maker-checker rules and reviewers' decisions. Reviewers change inputs, never
// totals; the engine recomputes the payable amount.
package review

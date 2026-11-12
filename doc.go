// Package netsec implements a concurrency-safe result queue for a Go security
// test host.
//
// A host enqueues Sample values; worker goroutines Claim a Lease, execute the
// single attempt it represents and report the outcome with Complete. Failed
// attempts are classified through the explicit Classifier interface:
//
//   - ClassInfrastructure is the only retryable class. The sample re-enters
//     the queue at the tail of its priority level, at most maxAttempts-1
//     times. When the budget is spent without a definitive outcome the sample
//     finishes with StatusInfrastructureExhausted.
//   - ClassCrash and every other terminal class finish the sample immediately
//     so that a real crash is never masked by an infrastructure retry.
//
// Every attempt is retained in the terminal Result in execution order,
// including failed attempts that were retried.
//
// Scheduling is priority-first (larger Sample.Priority wins) and FIFO within
// a priority level by enqueue tick. Retries take a fresh tick and therefore
// line up behind samples admitted while they were running; retries can never
// starve newly enqueued work.
//
// Each (SampleID, AttemptNumber) pair is leased at most once and Complete
// rejects any lease that is unknown or has already been consumed, so an
// attempt is never executed twice even under concurrent claimants.
package netsec

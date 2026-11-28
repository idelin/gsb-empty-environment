// Package retry provides the runtime admission and retry decision maker used
// by a service-mesh sidecar on every directed edge of a multi-hop call graph.
//
// The package is deliberately split into small, testable pieces:
//
//   - SlidingWindow counts normal requests inside a rolling time window.
//   - RetryBudget enforces allowedRetries = max(MinRetryConcurrency,
//     floor(BudgetRatio * activeNormalRequests)) and tracks in-flight retries.
//   - CircuitBreaker implements the Closed/Open/HalfOpen state machine.
//   - Backoff implements exponential backoff with deterministic (seeded,
//     reproducible) jitter.
//   - EdgeController combines every signal (budget, breaker, deadline,
//     backoff, hedging) into a single retry/hedge/abort decision per call.
//   - Executor drives a real invoke function through the controller,
//     including hedged branches that race each other and cancel on first
//     success.
//
// All time-dependent components take a Clock, so the same logic runs against
// a wall clock in production and against SimScheduler in deterministic tests
// and in the steady package discrete-event simulation.
package retry

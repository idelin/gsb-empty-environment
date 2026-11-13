// Package netsec provides an importable state machine that enforces the
// max_early_data byte ceiling on TLS 1.3 0-RTT early data.
//
// A TLS stack feeds connection events to [Machine.Handle] and applies the
// returned [Action] directives to its application-facing read path. The
// machine guarantees that:
//
//   - Early-data bytes whose cumulative length stays within the ticket's
//     max_early_data produce [ActionDeliver] and may reach the application.
//   - The first record that crosses the ceiling is split at the boundary: the
//     in-limit prefix is delivered and the over-limit suffix is withheld.
//   - Once the ceiling is exceeded the machine enters [StateFailed], and every
//     subsequent early-data record produces [ActionWithhold]. Already
//     decrypted excess bytes therefore can never be handed to the application.
//
// Only [ActionDeliver] payloads are deliverable. The machine is safe for
// concurrent use because TLS records may be processed on separate goroutines.
package netsec

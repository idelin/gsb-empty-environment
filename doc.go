// Package netsec compares the externally observable responses of a login
// endpoint for two account classes (for example "unknown account" and
// "known account, wrong password") so test hosts can verify that the classes
// are indistinguishable to an unauthenticated client.
//
// An Observation captures every relevant side channel:
//
//   - status code;
//   - response headers, including duplicate header names, their multiplicity
//     and per-index value order (see HeaderView);
//   - the body template, i.e. the response text with per-request random data
//     replaced by stable placeholders, so bodies are compared structurally;
//   - raw and Content-Encoding-aware compressed length bands;
//   - per-stage timing tiers, where a timeout is its own incomparable band.
//
// Lengths and timings are inclusive ranges or bands rather than point values.
// Two bands are equal when they overlap, which models the information actually
// available to an attacker.
//
// Compare walks every dimension independently and returns a Report whose
// Distinguishers are the minimal distinguishing condition: a disjunction of
// field-level predicates, any one of which is sufficient to tell the two
// account classes apart. A difference is suppressed only when its field is
// explicitly listed in the caller-supplied allowlist (FieldSelector); such
// suppressed predicates are still recorded in Report.Ignored. Fields the
// caller did not name can never be hidden, so a passing comparison cannot
// rely solely on identical final error text.
package netsec

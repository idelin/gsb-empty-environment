// Package netsec provides an importable label-constraint component for Go
// metrics services that must defend against cardinality-exhaustion attacks.
//
// A Guard enforces, per request and per tenant:
//
//   - an allowlist of label keys (KeyPolicy), each with value length limits;
//   - value allowlists with rejection or bounded bucketing for high-cardinality
//     dimensions, so user-controlled values can never create unbounded series;
//   - a cap on the number of distinct series per tenant;
//   - a token-bucket request-rate limit per tenant;
//   - cardinality-anomaly detection and bounded alert buffering.
//
// Every internal collection has an explicit size bound, which makes the
// component's memory footprint independent of attacker-controlled input.
package netsec

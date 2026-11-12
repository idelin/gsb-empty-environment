// Package vex implements trust decisions for Vulnerability Exploitability
// eXchange (VEX) statements used by vulnerability aggregation gates.
//
// VEX lets a component supplier assert that a vulnerability does not affect a
// shipped version. Because a forged not_affected statement can suppress a real
// alert, an Engine only adopts a statement after every trust condition holds:
//
//   - analysis.state is one of the CycloneDX states exploitable, in_triage,
//     false_positive, not_affected or resolved.
//   - not_affected carries a recognized non-empty justification; a missing
//     justification is rejected rather than trusted.
//   - the statement's affects list matches the component by both bom-ref and
//     a package URL whose version range contains the component version.
//   - the issuer is inside the trusted issuer set of the component supplier.
//   - the statement timestamp is no earlier than the component release date.
//
// Only an adopted not_affected statement suppresses an alert; every other
// state, and every rejected statement, leaves the original alert in place.
package vex

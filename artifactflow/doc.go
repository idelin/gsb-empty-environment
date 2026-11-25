// Package artifactflow verifies the artifact flow of a release gate from a
// set of already signature-verified build receipts.
//
// The gate treats receipts as unordered, signed evidence and decides whether
// the final manifest admits a globally consistent correspondence between
// receipts and the inputs/outputs declared by build steps. It then checks
// that every feasible correspondence attributes the same root-source set to
// each sensitive manifest artifact; two competing attributions are rejected
// as ambiguous authorization.
//
// # Model
//
// Each Step declares up to MaxCandidates mutually exclusive Candidates. A
// candidate binds input and output Slots (logical path, content digest and
// allowed signer roles) and optional rename Rules. At most one candidate of
// each step may be realized in a single build instance, so outputs taken from
// different alternatives can never be combined.
//
// Input slots are either consumed (single-use: the same receipt can be spent
// on at most one edge of the build instance) or shareable (free to fork).
// Every derivation retains the signature sources of all its inputs, and the
// root-source set of an artifact is the set of RootSource values reachable
// through the realized derivation edges.
//
// Rename rules match either full paths or patterns with one capture
// component (for example "dist/{name}.tar.gz"); a capture binds to the same
// concrete component on both ends of a rule. Path normalization only removes
// explicit current-directory (".") components and never permits escaping the
// tenant root.
//
// # Verdicts
//
// Verify returns a Report with one of:
//
//   - StatusOK: an unambiguous flow, with an independently verifiable
//     Certificate that bundles the accepted assignment and all evidence.
//   - StatusAmbiguous: two feasible correspondences disagree on the
//     root-source set of a sensitive entry; Report.Ambiguity carries both
//     source sets and full witness assignments.
//   - StatusInfeasible: no globally consistent correspondence exists.
//
// Certificates are re-checked with VerifyCertificate without re-running the
// build; callers may plug in a ReceiptVerifier and/or GateVerifier to
// re-validate signatures independently.
package artifactflow

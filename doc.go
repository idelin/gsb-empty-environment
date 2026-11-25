// Package artifactflow verifies artifact-flow receipts before a release gate
// authorizes a final manifest.
//
// The input is a ReceiptSet: already signature-verified build receipts plus
// external root artifacts and the requested final manifest. Receipts are
// grouped by Step and may appear in any order. A step has one to three
// alternative implementations; every declared step participates and exactly
// one alternative is selected for it. Outputs of different alternatives for
// the same step are never mixed.
//
// Every port names a logical path and a content digest. An artifact instance is
// identified by tenant, path, digest and producer, never by name alone. This
// prevents a later step from silently replacing a clean artifact with
// different content at the same name.
//
// An input port is either Share or Consume. A Share input may fork to many
// edges. A Consume input uses one concrete artifact instance at most once in a
// build; two steps consuming the same one-shot instance therefore make the
// build inconsistent.
//
// Edges may use an exact rename rule or a rule with exactly one capture
// component, such as "stage/{name}/app" -> "release/{name}/app". The captured
// component is constrained to be the same on both sides. The same rules also
// bind a step output to the input that produced it when the paths match, so
// captured paths cannot cross between outputs.
//
// Logical paths are normalized by removing explicit "." components only.
// Absolute paths, empty components and ".." are rejected; rename rules are
// tenant-local and can never cross a tenant root.
//
// Verify performs an exhaustive search over the bounded graph (at most 16
// steps and 3 candidates per step). It succeeds only when a globally consistent
// build exists and every feasible build attributes the same root provenance
// set to each sensitive final artifact. Two feasible attributions with
// different root sets produce StatusAmbiguous and two complete source groups.
//
// A successful Result carries a Certificate. VerifyCertificate checks it
// without invoking the solver and without executing any build step: it
// revalidates selections, edges, rename rules, consumption counts, cycles and
// recomputed provenance. Certificate body hashes make accidental edits
// evident.
package artifactflow

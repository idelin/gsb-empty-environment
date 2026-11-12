// Package netsec derives the in-scope dependency set of a CycloneDX SBOM and
// enforces a release license policy against it.
//
// Scope is computed by walking the dependencies graph from the root; the
// self-reported component scope is never trusted. Structural ambiguities
// (duplicate bom-refs, same-purl/different-version conflicts, dangling
// edges) make evaluation fail closed. SPDX license expressions are evaluated
// with AND/OR/WITH semantics; see the spdx subpackage for a standalone
// parser.
package netsec

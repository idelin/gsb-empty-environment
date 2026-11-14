// Package netsec implements generation-consistent field-level authorization
// for a search gateway.
//
// An alias expression is resolved to concrete indices against one immutable
// Catalog snapshot. Every concrete index policy (and optional alias policy) is
// intersected, so a field is returned only if every resolved target allows it.
// The resulting Plan binds the allow-list, resolved targets, and policy digest
// to that catalog generation.
//
// Authorize/Prepare sanitize field lists, _source, stored/doc-value fields,
// aggregations, script fields, runtime mappings, sort, and highlighting.
// FilterResponse applies the same plan to the backend response, including
// _source, fields, highlight, inner_hits, aggregations, and aggregation
// buckets. Request wildcards only narrow the effective set. Runtime fields and
// parent fields never add grants.
package netsec

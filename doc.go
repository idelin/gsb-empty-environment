// Package netsec provides a pre-expansion budget for YAML configuration
// documents that use anchors, aliases, and << merge keys.
//
// The checker walks a yaml.v3 Node graph and memoizes each distinct node, so
// estimating an exponential alias DAG is linear in the source graph rather than
// in the expanded tree. Aliases remain pointers and merge keys remain in place;
// a document that passes validation can therefore be decoded by yaml.v3 with
// its normal merge semantics and original Line/Column metadata.
package netsec

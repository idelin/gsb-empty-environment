// Package netsec implements an allowlisted decoder for untrusted,
// self-describing cache payloads.
//
// A payload names an exact type identifier and version. Decode resolves that
// pair against an explicit Registry, validates that the data object contains
// only registered fields, enforces nesting and reference limits, constructs
// only the registered concrete Go type, and finally applies range and
// cross-field consistency validation.
package netsec

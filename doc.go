// Package netsec implements fail-closed, field-level authorization planning
// for Go GraphQL gateways.
//
// The planner expands fragments, keeps every alias occurrence independent,
// binds each occurrence to its exact response path, evaluates @skip and
// @include against runtime variables, and calls a Policy for every reachable
// occurrence. Duplicate aliases that share a response path are merged only
// when both caller identity and the full authorization result match.
package netsec

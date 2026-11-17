// Package netsec provides a budgeted comparison gate for restricted query
// hosts.
//
// A host constructs one Gate with its trusted comparator and an authorization
// budget. Plugins receive only the Gate. Every comparison is bound to a query
// ID, field, purpose, and idempotency request ID. Budget is reserved
// atomically before the comparator runs. Successful results, errors, and
// comparator panics are cached under the request ID; reusing that ID for a
// different operation is rejected. Failures never refund budget, so repeatedly
// forcing failures cannot create extra probing opportunities.
package netsec

// Package netsec decodes a small object graph with explicit own and borrow
// edges. It validates the entire graph before invoking any constructor, then
// returns an idempotently closable root handle.
package netsec

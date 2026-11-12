// Package netsec provides a budgeted CBOR reader for decoding gateway
// requests. Decoding enforces caller-selected limits on tag nesting, tag 24
// recursion, indefinite string chunks, declared lengths, bignums and total
// nesting, and rejects duplicate map keys.
package netsec

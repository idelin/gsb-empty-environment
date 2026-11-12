// Package netsec redacts kernel address leakages from diagnostic output.
//
// It recognizes prefixed and unprefixed kernel pointers, %pK/%px output,
// and little-endian byte dumps, replacing each detected address with a
// fixed-length placeholder. Redact provides batch processing, while
// Redactor implements chunk-boundary-safe streaming through io.Writer.
package netsec

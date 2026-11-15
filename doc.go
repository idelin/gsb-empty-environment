// Package netsec provides a structural gate for Ruby Marshal 4.8 streams.
//
// The gate walks a stream byte by byte and never asks Ruby (or any other
// runtime) to deserialize, allocate, or instantiate an object. Only the
// following Marshal type codes are accepted:
//
//	0 T F i " : ; [ { I @
//
// and o objects whose class name appears in the caller-provided whitelist.
// User-marshal (u), marshal_load (U), extended (e), subclassed core (C),
// data (d), struct (S), old module (M), and all other type codes are rejected.
// Forward or out-of-range symbol/object links, excessive nesting, excessive
// value counts, truncated input, and trailing data are also rejected.
package netsec

// Package jsonpathstream evaluates JSONPath queries against JSON that arrives
// in arbitrary blocks, without buffering the whole document.
//
// The streaming engine (EvaluateReader) drives encoding/json token by token and
// delivers each match to a Sink as soon as its value is known. Scalar matches
// are emitted immediately. A subtree is materialized only when a query cannot
// be resolved online, namely:
//   - a filter segment, because the fields its predicate references may appear
//     later in the same object than the value to output; or
//   - a negative array index/slice, whose position depends on the array
//     length; or
//   - a container (object/array) selected in full, which must be materialized
//     to produce its value.
//
// In every case at most the current candidate subtree is held at a time, so
// memory stays bounded by the largest individual node rather than the document
// size. DecodeAll plus EvaluateAll provide the whole-document reference
// implementation; the test suite asserts both produce identical ordered
// results across many queries and input block sizes.
package jsonpathstream

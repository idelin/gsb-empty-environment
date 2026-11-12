// Package netsec plans safe multi-range byte reads for a download gateway.
//
// Range requests can be abused for response amplification: many overlapping
// intervals make the gateway return the same object bytes repeatedly. The
// package defends against that by:
//
//   - parsing closed (start-end), open-tail (start-) and suffix (-suffix)
//     byte intervals from a Range header;
//   - normalizing each interval against the fixed length of the target
//     object, reporting unsatisfiable intervals explicitly;
//   - de-duplicating identical response parts while preserving the distinct
//     requested ranges as separate response semantics;
//   - coalescing overlapping and adjacent spans into a minimal set of
//     physical object reads;
//   - enforcing limits on segment count, payload bytes, wire bytes and read
//     count, with overflow-checked arithmetic; and
//   - accounting for multipart/byteranges boundary and part-header overhead
//     in the wire budget via MultipartEstimator.
package netsec

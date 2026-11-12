// Package taskpkg provides an incremental, byte-oriented CSV reader for
// ingestion pipelines.
//
// The reader keeps quote and CRLF state across reads. It reports each decoded
// field together with its absolute raw byte interval, and reports the
// physical line interval occupied by each record. Malformed, truncated, and
// oversized records are consumed to their next record boundary and returned as
// RecordError values without any partial fields.
package taskpkg

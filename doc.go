// Package netsec provides a streaming RESP request filter and canonical
// re-encoder for a Redis proxy. It parses array-style and inline requests,
// applies a case-insensitive command allow-list, and rewrites every allowed
// request as a flat array of bulk strings whose lengths are recomputed from the
// decoded byte content. Rejected requests never reach the backend; instead the
// caller receives a fixed synthetic error reply for that exact pipeline slot.
package netsec

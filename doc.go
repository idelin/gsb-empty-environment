// Package netsec implements admission control for HTTP/2 server push that is
// forwarded by a proxy.
//
// Authentication context is never inferred from a PUSH_PROMISE. A parent
// request is registered with a snapshot of the origins (sources) for which its
// authentication context may be used. Every promise is then checked against
// that parent, the connection generation, and the exact promised origin before
// an allocation or business callback is allowed to run.
//
// The package deliberately treats a promised stream ID as consumed as soon as a
// syntactically valid PUSH_PROMISE is observed. This includes promises that
// later fail authorization, which prevents a stream ID from being retried with
// a different source context. Cancellation of a parent request and
// cancellation of an individual push are recorded as distinct lifecycle
// events.
package netsec

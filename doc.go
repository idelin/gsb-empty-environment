// Package netsec provides authenticated trace-context propagation for Go HTTP
// middleware.
//
// Inbound traceparent and tracestate headers are parsed with strict W3C
// version 00 rules and size limits. Tenant, origin, timestamp, and trace
// identifiers are covered by an HMAC-SHA256 signature. Only a context that
// passes format, size, signature, freshness, and local tenant/origin binding
// checks may contribute its trace ID. Every request receives a newly generated
// local span ID, and sampling is decided by local policy; inbound trace flags
// are retained for audit only.
//
// The middleware also replaces inbound tracing headers with a freshly signed
// local context before the application handler runs, preventing accidental
// forwarding of attacker-controlled values. Use Tracer.Inject on outbound
// client requests.
package netsec

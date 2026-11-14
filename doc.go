// Package netsec provides an early-data (0-RTT) admission component for
// Go HTTP/3 edge gateways. Tickets bind the session, client identity,
// SNI, ALPN and ticket generation; only idempotent methods or requests
// carrying a one-time idempotency key are admitted in the first flight,
// everything else is deferred until the handshake completes. Admitted
// requests are deduplicated by identity and idempotency key in a replay
// cache so replays wait for the in-flight result instead of
// re-executing side effects. Ticket rotation, revocation and policy
// tightening invalidate previously issued tickets.
package netsec

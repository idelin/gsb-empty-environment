// Package netsec provides fail-closed upstream selection for a local DNS stub
// resolver. It dispatches DNS messages to caller-configured DoH, DoT, and
// plaintext upstreams under either a strict or an opportunistic policy.
//
// Strict mode authenticates every DoH and DoT handshake with a caller-supplied
// certificate name and SPKI pin, and never falls back to plaintext DNS. It also
// requires the response Content-Type to be application/dns-message, and checks
// the response transaction ID and question section against the request.
//
// Opportunistic mode applies the same checks to secure upstreams, records every
// plaintext fallback in Result.Degradation, and logs it through the configured
// slog logger.
package netsec

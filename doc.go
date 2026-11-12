// Package netsec implements fail-closed target admission for RFC 9298
// CONNECT-UDP proxies.
//
// ParseTarget / Admit validate the request path before DNS, require every
// resolved address to pass the built-in public-address policy and the caller's
// allowlist, and return fixed addresses for dialing. UDPPayload discards
// HTTP Datagrams whose Context ID is not zero.
package netsec

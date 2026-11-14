// Package netsec provides an outbound HTTP policy for cloud services.
//
// A Policy combines a destination allowlist with address-level checks. It is
// intended for requests whose URL can be influenced by a user, such as a
// callback URL. The policy:
//
//   - allows only configured domain/port pairs (and, when explicitly listed,
//     IP-literal destinations);
//   - rejects link-local, cloud metadata, multicast, and other unsafe networks
//     both before and after DNS resolution;
//   - resolves a hostname once, validates every returned address, and pins the
//     connection to those addresses to prevent DNS rebinding;
//   - ignores environment and explicit HTTP proxies;
//   - rejects Host-header overrides and does not follow redirects; and
//   - injects configured credentials only after the final target passes the
//     policy checks.
//
// The zero value of Policy is not usable. Construct one with New or
// NewPolicy.
package netsec

// Package netsec implements a redirect-aware download planner for Go
// module download hosts that keeps private-source credentials from
// leaking into public mirrors.
//
// Security model:
//
//   - Identity is bound to a precise origin (scheme, host and port,
//     normalized) and to the request purpose (artifact download versus
//     metadata fetch).
//   - At every hop the target's canonical origin is recomputed and the
//     credential is re-selected from the caller-owned CredentialStore.
//     Credentials are never copied from a previous request.
//   - Origin responses cannot introduce credentials: userinfo embedded
//     in a Location header is stripped, authentication headers sent on
//     prior hops are removed before re-selection, and WWW-Authenticate
//     challenges never trigger credential generation. Only explicit
//     CredentialStore rules supplied by the calling program authorize a
//     request.
//   - When no rule matches an origin, the request proceeds without any
//     authentication header rather than falling back to another
//     origin's token.
package netsec

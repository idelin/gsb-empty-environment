// Package netsec implements the server-side device authorization grant flow.
//
// A Service binds each device code to one client, its requested scopes, a
// user-facing code, a polling interval, an expiry time, and the subject who
// approves it. Approval freezes the granted scopes. Redemption is atomic,
// single-use, and can only return a subset of the frozen approval.
package netsec

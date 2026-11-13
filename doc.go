// Package netsec validates OpenID Connect back-channel logout tokens.
//
// A Validator authenticates compact JWTs, enforces the logout-token profile
// (typ, events, sub/sid, nonce, jti), and records jti values for ten minutes
// to reject replays. The returned LogoutToken tells the relying party whether
// to terminate one session or all sessions for a subject.
package netsec

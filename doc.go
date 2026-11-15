// Package netsec provides the WPA3-SAE commit pre-filter used by an access
// point Authentication frame handler.
//
// A commit is admitted only after the scalar and element have passed the
// cheap protocol checks. When too many SAE transactions are incomplete, the
// filter does not validate curve material at all: it returns a rotating
// HMAC anti-clogging token, and the caller must send only that token. A
// retry carrying the token is then admitted exactly once.
package netsec

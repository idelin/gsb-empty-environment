// Package netsec provides an immutable HTTP middleware chain for reverse
// proxies.
//
// A Chain always runs its stages in this order:
//
//	sanitize -> authenticate -> inject identity -> custom middleware -> forward
//
// The custom middleware supplied at construction time is therefore only ever
// placed after identity injection. No public API can move a stage or insert a
// middleware before authentication.
package netsec

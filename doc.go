// Package netsec provides a concurrency-safe session lifecycle component for
// Go authentication services.
//
// Sessions are created only after authentication succeeds. Every session
// identifier is generated from crypto/rand, bound to a device, and limited by
// both an idle timeout and an absolute lifetime. Successful authentication,
// privilege elevation, and tenant switching rotate the identifier and
// invalidate the previous one before the operation returns.
package netsec

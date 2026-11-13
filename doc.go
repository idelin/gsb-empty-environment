// Package netsec implements admission control for fixed-name objects in a
// multi-session object namespace.
//
// A name is scoped to either Global or one session and carries a canonical
// UTF-16 path plus an object type. The Namespace stores immutable creation
// metadata and uses atomic create-if-absent semantics. Service binds each
// expected name to a policy, saves the identity of the object it opened or
// created, and revalidates that identity before every host callback.
//
// The package is an in-process model of a trusted kernel namespace: the
// creator SID and integrity supplied in CreateRequest represent metadata that
// the real host obtains from the kernel, not values a low-privilege process
// may forge.
package netsec

// Package netsec implements endpoint registration and connection
// handshaking for a local inter-process communication host.
//
// Endpoint names map to random instance identities. Re-registering a name
// after deletion mints a brand new InstanceID and revokes the previous
// generation, so an existing connection can never impersonate the
// replacement service instance.
//
// Every accepted Conn is bound immutably at creation time to:
//   - the endpoint instance that owned the name during the handshake;
//   - the peer credentials observed on the transport at that moment;
//   - a fresh random Challenge the peer must prove possession of through
//     an asynchronous Authenticator callback.
//
// Revocation (explicit or caused by a same-name rebuild) blocks all new
// message admission, while messages already accepted stay pinned to the
// fixed instance that handled them and can finish normally.
package netsec

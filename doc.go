// Package netsec implements request-scoped client impersonation for a Go
// service host.
//
// The host owns a fixed primary token. A Thread holds an explicit stack of
// tokens issued by the Authenticator, and every token is bound to a connection
// ID, request ID, generation, authority level, and host-monotonic deadline.
// Enter verifies that the token is live for the thread's current request;
// Exit pops exactly the top token. Identification can read identity only,
// Impersonation can reach local resources, and Delegation is required for
// remote calls.
//
// Async work must choose NoIdentity or Capture explicitly. Capture produces an
// immutable snapshot and normally downgrades to Identification. Retaining
// Impersonation or Delegation requires an unforgeable host Grant, a task
// deadline no later than the request deadline, and a task handle bound to the
// request generation. Request end and connection reuse revoke that generation,
// and both callback start and every resource call revalidate the handle.
//
// Thread.RunScoped and RunScopedContext restore the exact prior stack on panic
// or cancellation instead of calling a broad RevertToSelf, so an inner failure
// cannot discard an outer impersonation frame.
package netsec

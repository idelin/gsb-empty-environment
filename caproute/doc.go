// Package caproute implements a host-side capability router that lets
// untrusted plug-in instances reach file and network resources through
// opaque, unforgeable handles.
//
// Capabilities can only be minted by the host (NewCapability). A handle is
// an opaque random token bound to a plug-in instance, a resource type, an
// allowed operation set and a non-rewindable generation. Plug-ins may hand
// arbitrary bytes to the router; anything that is not a minted handle is
// rejected without looking anything up.
//
// Every host-mediated invocation carries a CallChain. Each nested proxy hop
// pushes a frame whose permission is intersected with the whole chain, so a
// high-privilege proxy can never widen a lower-privilege caller. Delegation
// can only narrow rights. Re-entrant (cyclic) chains are rejected and a
// chain may contain at most MaxChainDepth frames.
//
// Instances can be unloaded and reloaded: reloading bumps a monotonic
// generation, which invalidates every previously minted handle. Closing a
// resource rejects new entries immediately, while operations that already
// acquired their lease are allowed to finish; Close only returns once all
// in-flight operations have drained.
//
// Every rejection is recorded with a structured reason via the audit sink.
// Error values returned to callers never disclose the identity of resources
// belonging to other tenants.
package caproute

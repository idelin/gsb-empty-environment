// Package netsec implements an EAP-style authenticator state package for an
// access host.
//
// The model has two identities:
//
//   - An outer identity is carried in the clear and is used only by a proxy to
//     select a route. It is never an authorization principal.
//   - An inner identity is carried inside a protected tunnel and is the only
//     identity that can become an authorization principal.
//
// A successful inner authentication is not a bearer token. It is represented by
// a Grant that is bound to all of the following:
//
//   - the protected tunnel's exporter value,
//   - a digest of the protected inner EAP exchange,
//   - the exact physical port attachment instance,
//   - the tunnel instance and its protected inner identity.
//
// Consequently, a success from one tunnel cannot be applied to another tunnel,
// even when both tunnels use the same outer identity and physical port name.
// Reattaching a physical port creates a new instance and invalidates every
// tunnel and grant from the previous attachment.
package netsec

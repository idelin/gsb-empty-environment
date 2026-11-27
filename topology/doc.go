// Package topology implements Kubernetes-style topology aware routing on top of
// EndpointSlice-like inputs.
//
// The package is split into two cooperating pieces:
//
//   - AllocateHints is the control-plane allocator. It distributes ready
//     endpoints across active availability zones using each zone's CPU share
//     and an overload threshold, emits forZones hints, and falls back to
//     global routing (all hints cleared) whenever a topology safeguard
//     triggers. Every fallback carries a precise ReasonCode and message.
//   - CompileNodeRoutes is the node-local data-plane compiler. Given a target
//     node and (optionally) an allocation result it produces the final
//     weighted backend table, honoring internalTrafficPolicy (Cluster vs
//     Local), zone hints, EndpointConditions (ready/serving/terminating) and
//     the terminating-endpoint fallbacks.
//
// EndpointSlice inputs are merged and de-duplicated by endpoint IP before
// either piece runs, so callers may pass overlapping slices safely.
package topology

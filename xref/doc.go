// Package xref resolves cross-namespace references for shared-gateway style
// controllers.
//
// It covers the two reference surfaces exposed by a shared gateway:
//
//   - HTTPRoute (or any route) backendRefs that point at Services in other
//     namespaces. Such a reference is only allowed when the target namespace
//     contains a ReferenceGrant whose "from" entry matches the route on group,
//     kind and namespace (all three, exactly), and whose "to" entry has the
//     same kind and either an empty name (wildcard) or the exact backend name.
//   - Listener allowedRoutes attachment rules: Same (the default), All and
//     Selector, where Selector matches labels on the route's namespace.
//
// Decisions are cached with explicit dependency tracking. Revoking a grant
// (or changing namespace labels) immediately purges every cached decision
// that depended on it, so a previously resolved reference reports invalid on
// the next check without waiting for a resync.
//
// The package only depends on the Go standard library and is safe for
// concurrent use.
package xref

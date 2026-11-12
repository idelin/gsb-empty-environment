// Package netsec is an importable, dependency-free evaluator for a focused
// subset of Istio AuthorizationPolicy semantics.
//
// Evaluation order for a workload is fixed:
//
//   - CUSTOM policies first; a matched rule delegates to a registered
//     CustomAuthorizer and failure from it denies the request.
//   - DENY policies next; the first matching rule denies immediately.
//   - ALLOW policies last; the request is allowed only if an allow rule
//     matches.
//
// Defaults follow the AuthorizationPolicy model:
//
//   - A workload with no policies at all allows every request.
//   - When only ALLOW policies exist and no rule matches, the request is
//     denied.
//   - An ALLOW policy with an empty rule set denies everything.
//
// Identity sources are kept separate and are never interchangeable:
//
//   - principals/notPrincipals consult peer SPIFFE identities extracted from
//     the verified client certificate (mTLS).
//   - requestPrincipals/notRequestPrincipals consult JWT identities built
//     from the verified token's iss and sub via JWTPrincipal.
//
// Security fix: a negative principal match cannot match an absent identity.
// A plaintext request has no peer identity, so notPrincipals evaluates to
// false rather than true and the Result carries a Warning. The same
// fail-closed treatment applies to notRequestPrincipals without a JWT. This
// prevents "allow all except service X" policies from being bypassed by
// sending unauthenticated plaintext traffic.
package netsec

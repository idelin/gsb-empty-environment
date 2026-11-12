// Package policydiag is imported by the Go release service to diagnose a
// finite-domain file-decryption policy before it is published.
//
// The policy universe is fully finite:
//
//   - subjects are key identities (public keys), several of which may belong
//     to the same human person (key aliases);
//   - every subject carries attributes: roles and a clearance level;
//   - files carry a classification level;
//   - the operation set is the finite set declared on the policy.
//
// Rules are allow, deny and require-dual-approval rules. Deny takes
// precedence over allow. A dual-approval requirement can only be discharged by
// two approvers whose key identities belong to distinct persons, so one person
// can never satisfy the requirement through two of their own aliases.
//
// # Conflict semantics
//
// A conflict group is a minimal correction set of rules: removing every rule
// in the group makes all business requirements (availability) and security
// requirements (no unauthorized access) hold at once, while removing any
// proper subset still leaves a violation. Each group carries a witness built
// from concrete domain values - the subject key, file id and, where dual
// approval applies, two approver key identities belonging to distinct
// persons.
//
// When the enumeration budget is exhausted the report sets BudgetExhausted and
// marks groups Minimal=false, because minimality can no longer be proven. If
// no rule deletion can repair the policy the concrete failures are returned in
// Unfixable instead of inventing a conflict group.
package policydiag

// Package netsec implements admission control for GraphQL queries at a
// gateway: it computes an exact worst-case cost upper bound before any field
// resolver runs and denies requests that exceed the caller's budget or touch
// fields outside the caller's permission scopes.
//
// The admitter works on a parsed query AST (see ast.go), request variables, a
// finite schema with per-field costs and list maximum lengths (see schema.go),
// and the caller's granted scopes. Its semantics mirror GraphQL execution:
//
//   - Fragments (named, inline and spread) and the conditional @skip/@include
//     directives are fully expanded; fragment cycles and undefined fragments
//     are rejected as validation errors.
//   - Fields that resolve to the same response key (alias or field name) at a
//     concrete object type are merged into one resolver group. Conflicting
//     selections, including different field names or unequal arguments after
//     variable substitution, are rejected.
//   - A field resolver is charged once per parent instance. Nested lists are
//     billed by multiplying the enclosing maximum lengths, so a field nested
//     under lists of sizes L1, L2 runs at most L1*L2 times. Multi-dimensional
//     list fields multiply each declared dimension.
//   - Abstract return types (interfaces and unions) are evaluated against
//     every possible concrete type; the most expensive branch sets the bound
//     so it holds for any runtime object.
//   - Authorization always uses the real schema field name and schema path,
//     never the alias, so aliasing cannot bypass field-level permissions.
//
// Admit returns a Report whose UpperBound is the exact worst-case cost and
// whose Contributions break the bound down per merged field group with both
// response and schema paths. Integer arithmetic saturates at a large sentinel
// so pathological schemas cannot overflow the bound downward.
package netsec

// Package netsec implements NACM (RFC 8341) authorization decisions for a
// NETCONF device management component.
//
// A Policy contains ordered rule lists and the read, write and exec defaults.
// A rule list applies to a user when at least one of its groups is in the
// user's group set. Rule lists are evaluated in policy order, and each list's
// rules are evaluated in slice order. The first rule whose path covers the
// target node and whose access-operations contains the requested operation
// decides the request. Create, update and delete are write operations and use
// WriteDefault when no rule matches; read and exec use ReadDefault and
// ExecDefault respectively.
//
// A target node with DefaultDenyAll set never falls back to a default permit.
// An explicit matching permit still allows access, as required by RFC 8341.
//
// Example:
//
//	policy := netsec.Policy{
//		RuleLists: []netsec.RuleList{
//			{
//				Groups: []string{"operators"},
//				Rules: []netsec.Rule{
//					{Path: "/system/status", Operations: netsec.OpRead, Action: netsec.Permit},
//					{Path: "/system", Operations: netsec.OpAll, Action: netsec.Deny},
//				},
//			},
//		},
//		ReadDefault:  netsec.Deny,
//		WriteDefault: netsec.Deny,
//		ExecDefault:  netsec.Deny,
//	}
//
//	decision := policy.Check([]string{"operators"}, "/system/status/uptime", netsec.Read, false)
//
// The example places the narrow permit before the broad deny because the
// first matching rule wins; reversing those two rules would deny the status
// subtree.
package netsec

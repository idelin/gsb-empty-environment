// Package netsec implements exact-match authorization for Kubernetes-style
// resources and subresources.
//
// The important security property is that a rule for a resource does not
// implicitly cover any of that resource's subresources. In particular, a
// rule for pods/get never authorizes pods/exec, pods/attach,
// pods/portforward, or pods/log. Each subresource is declared explicitly.
// Empty fields are not wildcards; the "*" token must be written explicitly.
// Namespaces and API groups are separate dimensions, so a rule cannot borrow
// authority from another namespace or API group.
package netsec

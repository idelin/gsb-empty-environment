// Package netsec prevents privilege escalation through Kubernetes Role and
// role-binding admission requests.
//
// CheckRole validates a Role create or update. It calls the supplied
// Authorizer once for the "escalate" verb on the Role, and, if that is denied,
// checks every permission represented by the new Role's rules. CheckRoleBinding
// performs the analogous check for a binding: it first looks for "bind" on the
// referenced Role or ClusterRole, then verifies every referenced permission in
// the binding's namespace.
//
// Rule authorization is directional and covers apiGroups, resources (including
// subresources such as pods/exec), verbs, and resourceNames. A namespace-scoped
// binding never consults permissions from another namespace.
package netsec

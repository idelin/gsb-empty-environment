// Package netsec implements an importable admission-control policy that
// prevents tenants from hijacking cluster traffic by claiming addresses they
// do not own through Service external IPs, load-balancer status or
// ExternalName aliases.
//
// # Policy rules
//
// Every entry in spec.externalIPs and every IP entry of
// status.loadBalancer.ingress is validated as follows:
//
//   - It must be a literal IPv4 or IPv6 address. Octal (0177.0.0.1),
//     hexadecimal (0x7f.0.0.1) and leading-zero decimal (010.0.0.1) IPv4
//     spellings are rejected, as are truncated forms and bare integers.
//   - IPv4-mapped IPv6 addresses such as ::ffff:10.0.0.1 are reduced to
//     their embedded IPv4 address before any range decision, so the mapped
//     spelling cannot bypass IPv4 rules.
//   - The address must fall within an address range the caller says is
//     available to the Service's namespace (Options.AllowedCIDRs). An empty
//     allowed list rejects everything (fail closed).
//   - The address must not be in the cluster reserved set
//     (Options.Reserved: Service CIDRs, Pod CIDRs, node addresses and the
//     in-cluster DNS Service addresses), nor be a loopback, link-local,
//     multicast or unspecified address.
//
// For Services of type ExternalName, spec.externalName must not be an IP
// literal (including loose octal/hex spellings) and must not resolve, through
// an in-cluster DNS name ending in ".svc" or ".cluster.local", to a Service
// in another namespace.
//
// The status subresource is evaluated with exactly the same rules: an update
// sent to /status cannot bypass the policy by setting
// status.loadBalancer.ingress[].ip.
//
// # Usage
//
// Compile a Policy once at webhook startup with NewPolicy and call
// Policy.Validate for each admission request, passing SubresourceStatus when
// the request targets the status subresource. The package-level Validate
// helper compiles on every call and suits low-volume or one-off use.
//
// All violations are returned as a joined error whose elements are
// *FieldError values carrying the offending field path and reason.
//
// # Adapting to corev1.Service
//
// This package depends only on the Go standard library so it builds in
// environments without the Kubernetes modules. The local Service type mirrors
// corev1.Service field-for-field for the inspected paths; in a real webhook,
// map corev1.Service into this type or swap the type for corev1.Service using
// the same field paths.
package netsec

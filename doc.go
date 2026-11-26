// Package networkpolicy compiles Kubernetes NetworkPolicy objects into an
// immutable evaluator suitable for a CNI dataplane.
//
// The package models IPv4 and IPv6 dual-stack Pods, namespace labels, per-Pod
// named container ports, policy type inference, policy union, ipBlock
// exclusions, and the independent source-Egress and destination-Ingress
// decisions required for a five-tuple connectivity answer.
//
// The zero value of a NetworkPolicy's Ingress or Egress field is nil and means
// the field was omitted. An explicitly present but empty field is a non-nil
// empty slice. This distinction matters: an empty ingress rule list denies all
// ingress, whereas a list containing one empty rule allows all ingress.
package networkpolicy

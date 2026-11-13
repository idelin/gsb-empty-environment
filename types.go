package netsec

// This package intentionally has no dependency on k8s.io/api: in some
// admission-controller build environments the Kubernetes modules are not
// available. The Service type below mirrors the relevant subset of
// corev1.Service field-for-field. When k8s.io/api is available, callers can
// convert with an adapter (or replace this type with corev1.Service) because
// the field paths and semantics are identical.

// ServiceType describes the type of a Service.
type ServiceType string

const (
	ServiceTypeClusterIP    ServiceType = "ClusterIP"
	ServiceTypeNodePort     ServiceType = "NodePort"
	ServiceTypeLoadBalancer ServiceType = "LoadBalancer"
	ServiceTypeExternalName ServiceType = "ExternalName"
)

// ServiceSpec mirrors the portion of corev1.ServiceSpec that the admission
// policy inspects.
type ServiceSpec struct {
	// Type is the type of the Service.
	Type ServiceType

	// ExternalIPs is the list of IP addresses for which the Service accepts
	// traffic on top of the Service's internal IPs.
	ExternalIPs []string

	// ExternalName is the external reference that kubedns or equivalent will
	// return as a CNAME record for this Service. Only used when Type is
	// ExternalName.
	ExternalName string
}

// LoadBalancerIngress represents the locations where traffic to the
// load-balancer should arrive. Only the IP field is inspected here.
type LoadBalancerIngress struct {
	// IP is set for load-balancer ingress points that are IP based.
	IP string

	// Hostname is set for load-balancer ingress points that are DNS based.
	// It is not an IP literal and therefore is not validated by this policy.
	Hostname string
}

// LoadBalancerStatus carries the externally visible status of a load
// balancer.
type LoadBalancerStatus struct {
	Ingress []LoadBalancerIngress
}

// ServiceStatus mirrors corev1.ServiceStatus.
type ServiceStatus struct {
	LoadBalancer LoadBalancerStatus
}

// Service mirrors the portion of corev1.Service that the admission policy
// inspects.
type Service struct {
	// Namespace is the namespace of the Service. It is used to decide whether
	// an ExternalName points at a Service in another namespace.
	Namespace string

	Spec   ServiceSpec
	Status ServiceStatus
}

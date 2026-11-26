package networkpolicy

import (
	"fmt"
	"net/netip"
	"strings"
)

// Protocol is a Kubernetes transport protocol. The empty value is interpreted
// as TCP wherever Kubernetes specifies TCP as the default.
type Protocol = string

const (
	TCP          Protocol = "TCP"
	UDP          Protocol = "UDP"
	SCTP         Protocol = "SCTP"
	ProtocolTCP           = TCP
	ProtocolUDP           = UDP
	ProtocolSCTP          = SCTP
)

// PolicyType is one of the two directions in which a NetworkPolicy can isolate
// traffic.
type PolicyType = string

const (
	Ingress           PolicyType = "Ingress"
	Egress            PolicyType = "Egress"
	PolicyTypeIngress            = Ingress
	PolicyTypeEgress             = Egress
)

// LabelSelector is equality-based, like metav1.LabelSelector with only
// matchLabels. A nil or empty selector matches every object.
type LabelSelector = map[string]string

// IPBlock is a CIDR range with an optional set of excluded sub-ranges.
// Both IPv4 and IPv6 CIDRs are supported. Every entry in Except must be a
// valid subnet of CIDR and use the same address family.
type IPBlock struct {
	CIDR   string   `json:"cidr"`
	Except []string `json:"except,omitempty"`
}

// NetworkPolicyPeer identifies traffic sources or destinations. In Kubernetes
// IPBlock is mutually exclusive with the two selectors. When both selectors
// are present they are ANDed. An omitted NamespaceSelector means the policy's
// own namespace, while an explicitly present empty NamespaceSelector means all
// namespaces.
type NetworkPolicyPeer struct {
	IPBlock           *IPBlock      `json:"ipBlock,omitempty"`
	NamespaceSelector LabelSelector `json:"namespaceSelector,omitempty"`
	PodSelector       LabelSelector `json:"podSelector,omitempty"`
}

// Peer is a shorter alias for NetworkPolicyPeer.
type Peer = NetworkPolicyPeer

// NetworkPolicyPort describes one allowed port. Port is a numeric container
// port, or Name is a named port. EndPort, when non-zero, turns Port into the
// inclusive range [Port, EndPort]. Kubernetes forbids EndPort together with a
// named port.
type NetworkPolicyPort struct {
	Protocol Protocol `json:"protocol,omitempty"`
	Port     int32    `json:"port,omitempty"`
	Name     string   `json:"name,omitempty"`
	EndPort  int32    `json:"endPort,omitempty"`
}

// Port is a shorter alias for NetworkPolicyPort.
type Port = NetworkPolicyPort

// NumericPort constructs a policy port matching one TCP/UDP/SCTP port number.
func NumericPort(port int32) NetworkPolicyPort {
	return NetworkPolicyPort{Port: port}
}

// NumericPortRange constructs an inclusive numeric port range.
func NumericPortRange(start, end int32) NetworkPolicyPort {
	return NetworkPolicyPort{Port: start, EndPort: end}
}

// NamedPort constructs a policy port that resolves to a container port name on
// the relevant Pod (the destination for ingress and the source for egress).
func NamedPort(name string) NetworkPolicyPort {
	return NetworkPolicyPort{Name: name}
}

// NetworkPolicyIngressRule is one entry in NetworkPolicy.Ingress. An empty
// rule (no From and no Ports) allows all ingress traffic. From entries are
// ORed; each peer's selectors are ANDed; ports are ORed within themselves and
// ANDed with From.
type NetworkPolicyIngressRule struct {
	From  []NetworkPolicyPeer `json:"from,omitempty"`
	Ports []NetworkPolicyPort `json:"ports,omitempty"`
}

// IngressRule is a shorter alias for NetworkPolicyIngressRule.
type IngressRule = NetworkPolicyIngressRule

// NetworkPolicyEgressRule is the egress counterpart of
// NetworkPolicyIngressRule.
type NetworkPolicyEgressRule struct {
	To    []NetworkPolicyPeer `json:"to,omitempty"`
	Ports []NetworkPolicyPort `json:"ports,omitempty"`
}

// EgressRule is a shorter alias for NetworkPolicyEgressRule.
type EgressRule = NetworkPolicyEgressRule

// NetworkPolicy is an importable, dependency-free representation of a
// Kubernetes NetworkPolicy.
//
// PolicyTypes is optional. When it is empty, the policy always includes
// Ingress and includes Egress exactly when Egress is non-nil (that is, when an
// "egress" field was explicitly supplied, even as an empty list).
type NetworkPolicy struct {
	Name        string                     `json:"name,omitempty"`
	Namespace   string                     `json:"namespace,omitempty"`
	PodSelector LabelSelector              `json:"podSelector,omitempty"`
	PolicyTypes []PolicyType               `json:"policyTypes,omitempty"`
	Ingress     []NetworkPolicyIngressRule `json:"ingress,omitempty"`
	Egress      []NetworkPolicyEgressRule  `json:"egress,omitempty"`
}

// ContainerPort is the target of a Pod's named port. The zero Protocol means
// TCP.
type ContainerPort struct {
	ContainerPort int32    `json:"containerPort"`
	Protocol      Protocol `json:"protocol,omitempty"`
}

// Pod is the policy input for one Pod. IP and IPs are both accepted to make
// dual-stack data easy to model; Endpoints concatenates them. Ports maps a
// container port name to its numeric port and protocol.
type Pod struct {
	Name       string                   `json:"name"`
	Namespace  string                   `json:"namespace"`
	Labels     map[string]string        `json:"labels,omitempty"`
	IP         string                   `json:"ip,omitempty"`
	IPs        []string                 `json:"ips,omitempty"`
	Ports      map[string]ContainerPort `json:"ports,omitempty"`
	NamedPorts map[string]ContainerPort `json:"-"`
}

// Endpoints returns the Pod's configured IP addresses in a stable order.
func (p Pod) Endpoints() []string {
	out := make([]string, 0, len(p.IPs)+1)
	if strings.TrimSpace(p.IP) != "" {
		out = append(out, p.IP)
	}
	out = append(out, p.IPs...)
	return out
}

// Namespace supplies the labels used by namespaceSelector. Namespaces do not
// have to be pre-registered; an unregistered namespace simply has no labels.
type Namespace struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

func selectorMatches(selector LabelSelector, labels map[string]string) bool {
	for key, value := range selector {
		actual, exists := labels[key]
		if !exists || actual != value {
			return false
		}
	}
	return true
}

// PolicySelectsPod reports whether the policy selects pod. Namespace selection
// is exact and label selectors are equality based.
func PolicySelectsPod(policy NetworkPolicy, pod Pod) bool {
	return policy.Namespace == pod.Namespace && selectorMatches(policy.PodSelector, pod.Labels)
}

// SelectsPod reports whether this policy selects pod.
func (p NetworkPolicy) SelectsPod(pod Pod) bool {
	return PolicySelectsPod(p, pod)
}

func normalizeProtocol(protocol Protocol) (Protocol, error) {
	value := strings.ToUpper(strings.TrimSpace(string(protocol)))
	if value == "" {
		return TCP, nil
	}
	switch value {
	case TCP, UDP, SCTP:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func normalizePolicyType(policyType PolicyType) (PolicyType, error) {
	value := strings.ToUpper(strings.TrimSpace(string(policyType)))
	switch value {
	case "INGRESS":
		return Ingress, nil
	case "EGRESS":
		return Egress, nil
	default:
		return "", fmt.Errorf("unsupported policy type %q", policyType)
	}
}

func validatePortNumber(port int32) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is outside 1..65535", port)
	}
	return nil
}

// Allows reports whether addr belongs to this IPBlock after all exceptions
// have been removed. Invalid blocks simply allow nothing.
func (b IPBlock) Allows(addr netip.Addr) bool {
	prefixes, err := PrefixDifference(b.CIDR, b.Except)
	if err != nil {
		return false
	}
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

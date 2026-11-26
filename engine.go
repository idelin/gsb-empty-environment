package networkpolicy

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Endpoint is an explicit Pod or bare IP endpoint accepted by Evaluate. If Pod
// is non-nil it takes precedence over IP.
type Endpoint struct {
	Pod *Pod
	IP  netip.Addr
}

// PodEndpoint wraps a Pod as an Endpoint.
func PodEndpoint(pod *Pod) Endpoint {
	return Endpoint{Pod: pod}
}

// IPEndpoint wraps an IP address as an Endpoint.
func IPEndpoint(addr netip.Addr) Endpoint {
	return Endpoint{IP: addr}
}

// PodRef addresses a Pod in a Cluster by namespace and name.
type PodRef struct {
	Namespace string
	Name      string
}

// Engine is an immutable compiled NetworkPolicy set bound to a Cluster.
type Engine struct {
	cluster  *compiledCluster
	policies []compiledPolicy
}

// PolicyEngine is an alias for Engine.
type PolicyEngine = Engine

// PolicySet is an alias for Engine.
type PolicySet = Engine

type compiledPolicy struct {
	name            string
	namespace       string
	podSelector     LabelSelector
	isolatesIngress bool
	isolatesEgress  bool
	ingressRules    []compiledRule
	egressRules     []compiledRule
}

type compiledRule struct {
	peers []compiledPeer
	ports []compiledPort
}

type compiledPeer struct {
	ipBlock              *compiledIPBlock
	namespaceSelector    LabelSelector
	namespaceSelectorSet bool
	podSelector          LabelSelector
	podSelectorSet       bool
	all                  bool
}

type compiledPort struct {
	protocol Protocol
	number   int32
	named    bool
	name     string
	endPort  int32
}

type endpoint struct {
	pod  *compiledPod
	addr netip.Addr
}

func (e endpoint) addresses() []netip.Addr {
	if e.addr.IsValid() {
		return []netip.Addr{e.addr}
	}
	if e.pod != nil {
		return e.pod.addrs
	}
	return nil
}

// Compile validates a set of policies against a Cluster and returns an
// immutable evaluator. Compilation rejects malformed ipBlocks, invalid port
// ranges, named ports combined with endPort, unknown protocols, and peers that
// combine ipBlock with selectors.
func Compile(cluster *Cluster, policies []NetworkPolicy) (*Engine, error) {
	compiledCluster, err := compileCluster(cluster)
	if err != nil {
		return nil, err
	}
	compiledPolicies := make([]compiledPolicy, 0, len(policies))
	for index, policy := range policies {
		compiled, compileErr := compilePolicy(policy)
		if compileErr != nil {
			name := policy.Name
			if name == "" {
				name = strconv.Itoa(index)
			}
			return nil, fmt.Errorf("network policy %q: %w", name, compileErr)
		}
		compiledPolicies = append(compiledPolicies, compiled)
	}
	return &Engine{cluster: compiledCluster, policies: compiledPolicies}, nil
}

// NewEngine is an alias for Compile.
func NewEngine(cluster *Cluster, policies []NetworkPolicy) (*Engine, error) {
	return Compile(cluster, policies)
}

// CompilePolicies compiles policies against an empty Cluster. Pod names cannot
// be resolved, but bare-IP evaluation and all policy semantics still work.
func CompilePolicies(policies []NetworkPolicy) (*Engine, error) {
	return Compile(nil, policies)
}

func compilePolicy(policy NetworkPolicy) (compiledPolicy, error) {
	types, err := effectivePolicyTypes(policy)
	if err != nil {
		return compiledPolicy{}, err
	}
	compiled := compiledPolicy{
		name:            policy.Name,
		namespace:       policy.Namespace,
		podSelector:     cloneLabels(policy.PodSelector),
		isolatesIngress: types[Ingress],
		isolatesEgress:  types[Egress],
	}
	for index, rule := range policy.Ingress {
		compiledRule, compileErr := compileRule(rule.From, rule.Ports)
		if compileErr != nil {
			return compiledPolicy{}, fmt.Errorf("ingress rule %d: %w", index, compileErr)
		}
		compiled.ingressRules = append(compiled.ingressRules, compiledRule)
	}
	for index, rule := range policy.Egress {
		compiledRule, compileErr := compileRule(rule.To, rule.Ports)
		if compileErr != nil {
			return compiledPolicy{}, fmt.Errorf("egress rule %d: %w", index, compileErr)
		}
		compiled.egressRules = append(compiled.egressRules, compiledRule)
	}
	return compiled, nil
}

func effectivePolicyTypes(policy NetworkPolicy) (map[PolicyType]bool, error) {
	types := make(map[PolicyType]bool, 2)
	if len(policy.PolicyTypes) == 0 {
		types[Ingress] = true
		if policy.Egress != nil {
			types[Egress] = true
		}
		return types, nil
	}
	for _, raw := range policy.PolicyTypes {
		policyType, err := normalizePolicyType(raw)
		if err != nil {
			return nil, err
		}
		types[policyType] = true
	}
	return types, nil
}

// EffectivePolicyTypes returns the policy directions after Kubernetes default
// inference. Without an explicit policyTypes field, Ingress is always present
// and Egress is present exactly when the Egress field is non-nil.
func EffectivePolicyTypes(policy NetworkPolicy) ([]PolicyType, error) {
	types, err := effectivePolicyTypes(policy)
	if err != nil {
		return nil, err
	}
	result := make([]PolicyType, 0, len(types))
	if types[Ingress] {
		result = append(result, Ingress)
	}
	if types[Egress] {
		result = append(result, Egress)
	}
	return result, nil
}

// EffectivePolicyTypes returns the inferred policy directions for this policy.
func (p NetworkPolicy) EffectivePolicyTypes() ([]PolicyType, error) {
	return EffectivePolicyTypes(p)
}

func compileRule(peers []NetworkPolicyPeer, ports []NetworkPolicyPort) (compiledRule, error) {
	compiled := compiledRule{}
	for index, peer := range peers {
		compiledPeer, err := compilePeer(peer)
		if err != nil {
			return compiledRule{}, fmt.Errorf("peer %d: %w", index, err)
		}
		compiled.peers = append(compiled.peers, compiledPeer)
	}
	for index, port := range ports {
		compiledPort, err := compilePolicyPort(port)
		if err != nil {
			return compiledRule{}, fmt.Errorf("port %d: %w", index, err)
		}
		compiled.ports = append(compiled.ports, compiledPort)
	}
	return compiled, nil
}

func compilePeer(peer NetworkPolicyPeer) (compiledPeer, error) {
	hasNamespaceSelector := peer.NamespaceSelector != nil
	hasPodSelector := peer.PodSelector != nil
	if peer.IPBlock != nil && (hasNamespaceSelector || hasPodSelector) {
		return compiledPeer{}, fmt.Errorf("ipBlock cannot be combined with namespaceSelector or podSelector")
	}

	compiled := compiledPeer{
		namespaceSelector:    cloneLabels(peer.NamespaceSelector),
		namespaceSelectorSet: hasNamespaceSelector,
		podSelector:          cloneLabels(peer.PodSelector),
		podSelectorSet:       hasPodSelector,
	}
	if peer.IPBlock != nil {
		block, err := compileIPBlock(peer.IPBlock)
		if err != nil {
			return compiledPeer{}, fmt.Errorf("ipBlock: %w", err)
		}
		compiled.ipBlock = block
		return compiled, nil
	}
	if !hasNamespaceSelector && !hasPodSelector {
		// An empty peer object is the all-traffic peer.
		compiled.all = true
	}
	return compiled, nil
}

func compilePolicyPort(port NetworkPolicyPort) (compiledPort, error) {
	protocol, err := normalizeProtocol(port.Protocol)
	if err != nil {
		return compiledPort{}, err
	}
	compiled := compiledPort{
		protocol: protocol,
		number:   port.Port,
		endPort:  port.EndPort,
	}

	if strings.TrimSpace(port.Name) != "" {
		if port.Port != 0 {
			return compiledPort{}, fmt.Errorf("named port %q must not also set a numeric port", port.Name)
		}
		if port.EndPort != 0 {
			return compiledPort{}, fmt.Errorf("named port %q cannot use endPort", port.Name)
		}
		compiled.named = true
		compiled.name = port.Name
		return compiled, nil
	}

	if port.Port == 0 {
		if port.EndPort != 0 {
			return compiledPort{}, fmt.Errorf("endPort requires a numeric start port")
		}
		// No number means every port for the selected protocol.
		return compiled, nil
	}
	if err := validatePortNumber(port.Port); err != nil {
		return compiledPort{}, err
	}
	if port.EndPort != 0 {
		if err := validatePortNumber(port.EndPort); err != nil {
			return compiledPort{}, fmt.Errorf("endPort: %w", err)
		}
		if port.EndPort < port.Port {
			return compiledPort{}, fmt.Errorf("endPort %d is less than port %d", port.EndPort, port.Port)
		}
	}
	return compiled, nil
}

func (p *compiledPolicy) selects(pod *compiledPod) bool {
	if pod == nil || p.namespace != pod.namespace {
		return false
	}
	return selectorMatches(p.podSelector, pod.labels)
}

func (p *compiledPeer) selects(source *compiledPod, address []netip.Addr, policyNamespace string, namespaces map[string]map[string]string) bool {
	if p.all {
		return true
	}
	if p.ipBlock != nil {
		return p.ipBlock.containsAny(address)
	}
	if source == nil {
		return false
	}
	if p.namespaceSelectorSet {
		if !selectorMatches(p.namespaceSelector, namespaces[source.namespace]) {
			return false
		}
	} else if source.namespace != policyNamespace {
		return false
	}
	if p.podSelectorSet && !selectorMatches(p.podSelector, source.labels) {
		return false
	}
	return true
}

func (r *compiledRule) matchesPeer(target endpoint, policyNamespace string, namespaces map[string]map[string]string) bool {
	if len(r.peers) == 0 {
		return true
	}
	for index := range r.peers {
		if r.peers[index].selects(target.pod, target.addresses(), policyNamespace, namespaces) {
			return true
		}
	}
	return false
}

func (r *compiledRule) matchesPort(protocol Protocol, port int32, pod *compiledPod) bool {
	if len(r.ports) == 0 {
		return true
	}
	for index := range r.ports {
		candidate := &r.ports[index]
		if candidate.protocol != protocol {
			continue
		}
		if candidate.named {
			resolved, ok := pod.resolveNamedPort(candidate.name, protocol)
			if ok && resolved == port {
				return true
			}
			continue
		}
		if candidate.number == 0 {
			return true
		}
		if candidate.endPort == 0 {
			if port == candidate.number {
				return true
			}
			continue
		}
		if port >= candidate.number && port <= candidate.endPort {
			return true
		}
	}
	return false
}

// Evaluate answers the five-tuple question "may src send proto/dstPort to
// dst?". It requires both the source Pod's Egress policy decision and the
// destination Pod's Ingress policy decision to allow the packet. Same-Pod
// traffic (including a Pod addressing its own IP) is always allowed.
//
// src and dst accept Endpoint, Pod, *Pod, PodRef, netip.Addr, or a string.
// A string is first parsed as an IP address, then as "namespace/name", and
// finally as a unique Pod name. proto accepts Protocol/string and defaults to
// TCP when empty. dstPort accepts integer types, a numeric string, or the name
// of a port in the destination Pod's named-port map.
func (e *Engine) Evaluate(src, dst any, proto any, dstPort any) (bool, error) {
	source, err := e.resolveEndpoint(src)
	if err != nil {
		return false, fmt.Errorf("source: %w", err)
	}
	destination, err := e.resolveEndpoint(dst)
	if err != nil {
		return false, fmt.Errorf("destination: %w", err)
	}
	protocol, err := protocolValue(proto)
	if err != nil {
		return false, err
	}
	port, portName, err := portValue(dstPort)
	if err != nil {
		return false, err
	}
	if portName != "" {
		if destination.pod == nil {
			return false, fmt.Errorf("cannot resolve named destination port %q without a destination Pod", portName)
		}
		resolved, ok := destination.pod.resolveNamedPort(portName, protocol)
		if !ok {
			return false, fmt.Errorf("destination Pod %q has no %s named port %q", podKey(destination.pod.namespace, destination.pod.name), protocol, portName)
		}
		port = resolved
	}

	if samePodOrLoopback(source, destination) {
		return true, nil
	}

	egressAllowed, err := e.allowsEgress(source, destination, protocol, port)
	if err != nil {
		return false, err
	}
	if !egressAllowed {
		return false, nil
	}
	return e.allowsIngress(source, destination, protocol, port)
}

// Allows is a bool-only convenience wrapper around Evaluate.
func (e *Engine) Allows(src, dst any, proto any, dstPort any) bool {
	allowed, err := e.Evaluate(src, dst, proto, dstPort)
	return allowed && err == nil
}

// AllowsIngress evaluates only the destination Pod's ingress isolation rules.
// It is primarily useful for diagnosing which side of Evaluate denied a
// packet.
func (e *Engine) AllowsIngress(src, dst any, proto any, dstPort any) (bool, error) {
	source, err := e.resolveEndpoint(src)
	if err != nil {
		return false, fmt.Errorf("source: %w", err)
	}
	destination, err := e.resolveEndpoint(dst)
	if err != nil {
		return false, fmt.Errorf("destination: %w", err)
	}
	protocol, err := protocolValue(proto)
	if err != nil {
		return false, err
	}
	port, portName, err := portValue(dstPort)
	if err != nil {
		return false, err
	}
	if portName != "" {
		if destination.pod == nil {
			return false, fmt.Errorf("cannot resolve named destination port %q without a destination Pod", portName)
		}
		resolved, ok := destination.pod.resolveNamedPort(portName, protocol)
		if !ok {
			return false, fmt.Errorf("destination Pod has no %s named port %q", protocol, portName)
		}
		port = resolved
	}
	if samePodOrLoopback(source, destination) {
		return true, nil
	}
	return e.allowsIngress(source, destination, protocol, port)
}

// AllowsEgress evaluates only the source Pod's egress isolation rules.
func (e *Engine) AllowsEgress(src, dst any, proto any, dstPort any) (bool, error) {
	source, err := e.resolveEndpoint(src)
	if err != nil {
		return false, fmt.Errorf("source: %w", err)
	}
	destination, err := e.resolveEndpoint(dst)
	if err != nil {
		return false, fmt.Errorf("destination: %w", err)
	}
	protocol, err := protocolValue(proto)
	if err != nil {
		return false, err
	}
	port, portName, err := portValue(dstPort)
	if err != nil {
		return false, err
	}
	if portName != "" {
		if destination.pod == nil {
			return false, fmt.Errorf("cannot resolve named destination port %q without a destination Pod", portName)
		}
		resolved, ok := destination.pod.resolveNamedPort(portName, protocol)
		if !ok {
			return false, fmt.Errorf("destination Pod has no %s named port %q", protocol, portName)
		}
		port = resolved
	}
	if samePodOrLoopback(source, destination) {
		return true, nil
	}
	return e.allowsEgress(source, destination, protocol, port)
}

// IsIsolated reports whether at least one policy selects the Pod for the
// requested direction. policyType may be Ingress or Egress.
func (e *Engine) IsIsolated(pod any, policyType PolicyType) (bool, error) {
	resolved, err := e.resolveEndpoint(pod)
	if err != nil {
		return false, err
	}
	if resolved.pod == nil {
		return false, fmt.Errorf("a bare IP cannot be selected by a NetworkPolicy")
	}
	normalized, err := normalizePolicyType(policyType)
	if err != nil {
		return false, err
	}
	for index := range e.policies {
		policy := &e.policies[index]
		if !policy.selects(resolved.pod) {
			continue
		}
		if normalized == Ingress && policy.isolatesIngress {
			return true, nil
		}
		if normalized == Egress && policy.isolatesEgress {
			return true, nil
		}
	}
	return false, nil
}

func (e *Engine) allowsEgress(source, destination endpoint, protocol Protocol, port int32) (bool, error) {
	if source.pod == nil {
		// A bare source IP is not selected by any policy, so egress defaults
		// to allow.
		return true, nil
	}
	selected := false
	for index := range e.policies {
		policy := &e.policies[index]
		if !policy.isolatesEgress || !policy.selects(source.pod) {
			continue
		}
		selected = true
		for ruleIndex := range policy.egressRules {
			rule := &policy.egressRules[ruleIndex]
			if !rule.matchesPeer(destination, policy.namespace, e.cluster.namespaces) {
				continue
			}
			if rule.matchesPort(protocol, port, source.pod) {
				return true, nil
			}
		}
	}
	return !selected, nil
}

func (e *Engine) allowsIngress(source, destination endpoint, protocol Protocol, port int32) (bool, error) {
	if destination.pod == nil {
		// A bare destination IP is not selected by any policy, so ingress
		// defaults to allow.
		return true, nil
	}
	selected := false
	for index := range e.policies {
		policy := &e.policies[index]
		if !policy.isolatesIngress || !policy.selects(destination.pod) {
			continue
		}
		selected = true
		for ruleIndex := range policy.ingressRules {
			rule := &policy.ingressRules[ruleIndex]
			if !rule.matchesPeer(source, policy.namespace, e.cluster.namespaces) {
				continue
			}
			if rule.matchesPort(protocol, port, destination.pod) {
				return true, nil
			}
		}
	}
	return !selected, nil
}

func samePodOrLoopback(source, destination endpoint) bool {
	if source.pod != nil && destination.pod != nil {
		return source.pod.namespace == destination.pod.namespace && source.pod.name == destination.pod.name
	}
	if source.pod != nil && destination.pod == nil && destination.addr.IsValid() {
		for _, addr := range source.pod.addrs {
			if addr == destination.addr {
				return true
			}
		}
	}
	if destination.pod != nil && source.pod == nil && source.addr.IsValid() {
		for _, addr := range destination.pod.addrs {
			if addr == source.addr {
				return true
			}
		}
	}
	return source.pod == nil && destination.pod == nil &&
		source.addr.IsValid() && destination.addr.IsValid() && source.addr == destination.addr
}

func (e *Engine) resolveEndpoint(value any) (endpoint, error) {
	switch typed := value.(type) {
	case Endpoint:
		return e.resolveExplicitEndpoint(typed)
	case *Endpoint:
		if typed == nil {
			return endpoint{}, fmt.Errorf("nil endpoint")
		}
		return e.resolveExplicitEndpoint(*typed)
	case PodRef:
		return e.resolvePodRef(typed.Namespace, typed.Name)
	case *PodRef:
		if typed == nil {
			return endpoint{}, fmt.Errorf("nil pod reference")
		}
		return e.resolvePodRef(typed.Namespace, typed.Name)
	case Pod:
		pod, err := compilePod(&typed)
		if err != nil {
			return endpoint{}, err
		}
		return endpoint{pod: pod}, nil
	case *Pod:
		return e.resolvePodPointer(typed)
	case netip.Addr:
		if !typed.IsValid() {
			return endpoint{}, fmt.Errorf("invalid IP address")
		}
		return e.resolveAddress(typed.Unmap()), nil
	case string:
		return e.resolveString(typed)
	default:
		return endpoint{}, fmt.Errorf("unsupported endpoint type %T", value)
	}
}

func (e *Engine) resolveExplicitEndpoint(value Endpoint) (endpoint, error) {
	if value.Pod != nil {
		return e.resolvePodPointer(value.Pod)
	}
	if !value.IP.IsValid() {
		return endpoint{}, fmt.Errorf("endpoint has neither a Pod nor a valid IP")
	}
	return e.resolveAddress(value.IP.Unmap()), nil
}

func (e *Engine) resolvePodPointer(pod *Pod) (endpoint, error) {
	if pod == nil {
		return endpoint{}, fmt.Errorf("nil pod")
	}
	if compiled, ok := e.cluster.lookupQualified(pod.Namespace, pod.Name); ok {
		return endpoint{pod: compiled}, nil
	}
	compiled, err := compilePod(pod)
	if err != nil {
		return endpoint{}, err
	}
	return endpoint{pod: compiled}, nil
}

func (e *Engine) resolvePodRef(namespace, name string) (endpoint, error) {
	if compiled, ok := e.cluster.lookupQualified(namespace, name); ok {
		return endpoint{pod: compiled}, nil
	}
	return endpoint{}, fmt.Errorf("pod %q was not found", podKey(namespace, name))
}

func (e *Engine) resolveString(value string) (endpoint, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return endpoint{}, fmt.Errorf("empty endpoint")
	}
	if addr, err := netip.ParseAddr(trimmed); err == nil {
		return e.resolveAddress(addr.Unmap()), nil
	}
	if slash := strings.IndexByte(trimmed, '/'); slash >= 0 {
		return e.resolvePodRef(trimmed[:slash], trimmed[slash+1:])
	}
	compiled, err := e.cluster.lookupName(trimmed)
	if err != nil {
		return endpoint{}, err
	}
	return endpoint{pod: compiled}, nil
}

func (e *Engine) resolveAddress(addr netip.Addr) endpoint {
	if pod := e.cluster.lookupAddress(addr); pod != nil {
		return endpoint{pod: pod, addr: addr}
	}
	return endpoint{addr: addr}
}

func protocolValue(value any) (Protocol, error) {
	switch typed := value.(type) {
	case nil:
		return TCP, nil
	case string:
		return normalizeProtocol(typed)
	case fmt.Stringer:
		return normalizeProtocol(typed.String())
	default:
		return "", fmt.Errorf("unsupported protocol value %T", value)
	}
}

func portValue(value any) (int32, string, error) {
	switch typed := value.(type) {
	case nil:
		return 0, "", fmt.Errorf("destination port is required")
	case int:
		return checkedPort(int64(typed))
	case int8:
		return checkedPort(int64(typed))
	case int16:
		return checkedPort(int64(typed))
	case int32:
		return checkedPort(int64(typed))
	case int64:
		return checkedPort(typed)
	case uint:
		return checkedPort(int64(typed))
	case uint8:
		return checkedPort(int64(typed))
	case uint16:
		return checkedPort(int64(typed))
	case uint32:
		return checkedPort(int64(typed))
	case uint64:
		if typed > 65535 {
			return 0, "", fmt.Errorf("port %d is outside 1..65535", typed)
		}
		return checkedPort(int64(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, "", fmt.Errorf("destination port is required")
		}
		if number, err := strconv.ParseUint(trimmed, 10, 64); err == nil {
			if number > 65535 {
				return 0, "", fmt.Errorf("port %d is outside 1..65535", number)
			}
			return checkedPort(int64(number))
		}
		return 0, trimmed, nil
	default:
		return 0, "", fmt.Errorf("unsupported destination port type %T", value)
	}
}

func checkedPort(port int64) (int32, string, error) {
	if port < 1 || port > 65535 {
		return 0, "", fmt.Errorf("port %d is outside 1..65535", port)
	}
	return int32(port), "", nil
}

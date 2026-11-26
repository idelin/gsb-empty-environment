package networkpolicy

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Cluster is the Pod and Namespace inventory used to resolve names and
// namespace labels during compilation and evaluation.
type Cluster struct {
	Namespaces map[string]*Namespace `json:"namespaces,omitempty"`
	Pods       map[string]*Pod       `json:"pods,omitempty"`
}

// NewCluster returns an empty Cluster with initialized maps.
func NewCluster() *Cluster {
	return &Cluster{
		Namespaces: make(map[string]*Namespace),
		Pods:       make(map[string]*Pod),
	}
}

// AddNamespace adds a Namespace. It accepts Namespace or *Namespace. The map
// key is Namespace.Name.
func (c *Cluster) AddNamespace(value any) error {
	var namespace Namespace
	switch typed := value.(type) {
	case Namespace:
		namespace = typed
	case *Namespace:
		if typed == nil {
			return fmt.Errorf("cannot add a nil namespace")
		}
		namespace = *typed
	default:
		return fmt.Errorf("unsupported namespace value %T", value)
	}
	if strings.TrimSpace(namespace.Name) == "" {
		return fmt.Errorf("namespace name must not be empty")
	}
	if c.Namespaces == nil {
		c.Namespaces = make(map[string]*Namespace)
	}
	copyOfNamespace := namespace
	copyOfNamespace.Labels = cloneLabels(namespace.Labels)
	c.Namespaces[namespace.Name] = &copyOfNamespace
	return nil
}

// AddNamespaceLabels is a convenience for registering namespace labels.
func (c *Cluster) AddNamespaceLabels(name string, labels map[string]string) error {
	return c.AddNamespace(Namespace{Name: name, Labels: labels})
}

// AddPod adds a Pod. It accepts Pod or *Pod. The map key is
// "namespace/name" (or just "name" when Namespace is empty). The Pod fields
// are copied, so later mutations by the caller do not affect the cluster.
func (c *Cluster) AddPod(value any) error {
	var pod Pod
	switch typed := value.(type) {
	case Pod:
		pod = typed
	case *Pod:
		if typed == nil {
			return fmt.Errorf("cannot add a nil pod")
		}
		pod = *typed
	default:
		return fmt.Errorf("unsupported pod value %T", value)
	}
	if strings.TrimSpace(pod.Name) == "" {
		return fmt.Errorf("pod name must not be empty")
	}
	if c.Pods == nil {
		c.Pods = make(map[string]*Pod)
	}
	key := podKey(pod.Namespace, pod.Name)
	if _, exists := c.Pods[key]; exists {
		return fmt.Errorf("pod %q is already registered", key)
	}
	copyOfPod := clonePod(pod)
	c.Pods[key] = &copyOfPod
	return nil
}

func podKey(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

func clonePod(pod Pod) Pod {
	copyOfPod := pod
	copyOfPod.Labels = cloneLabels(pod.Labels)
	copyOfPod.IPs = append([]string(nil), pod.IPs...)
	if pod.Ports != nil {
		copyOfPod.Ports = make(map[string]ContainerPort, len(pod.Ports))
		for name, port := range pod.Ports {
			copyOfPod.Ports[name] = port
		}
	}
	if pod.NamedPorts != nil {
		copyOfPod.NamedPorts = make(map[string]ContainerPort, len(pod.NamedPorts))
		for name, port := range pod.NamedPorts {
			copyOfPod.NamedPorts[name] = port
		}
	}
	return copyOfPod
}

func cloneLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

type compiledPod struct {
	namespace string
	name      string
	labels    map[string]string
	addrs     []netip.Addr
	ports     map[string]ContainerPort
}

func (p *compiledPod) resolveNamedPort(name string, protocol Protocol) (int32, bool) {
	if p == nil {
		return 0, false
	}
	port, ok := p.ports[name]
	if !ok {
		return 0, false
	}
	normalized, err := normalizeProtocol(port.Protocol)
	if err != nil || normalized != protocol {
		return 0, false
	}
	return port.ContainerPort, true
}

type compiledCluster struct {
	namespaces  map[string]map[string]string
	byQualified map[string]*compiledPod
	byName      map[string][]*compiledPod
	byIP        map[netip.Addr][]*compiledPod
}

func compileCluster(cluster *Cluster) (*compiledCluster, error) {
	out := &compiledCluster{
		namespaces:  make(map[string]map[string]string),
		byQualified: make(map[string]*compiledPod),
		byName:      make(map[string][]*compiledPod),
		byIP:        make(map[netip.Addr][]*compiledPod),
	}
	if cluster == nil {
		return out, nil
	}
	for key, namespace := range cluster.Namespaces {
		if namespace == nil {
			return nil, fmt.Errorf("namespace %q is nil", key)
		}
		name := namespace.Name
		if name == "" {
			name = key
		}
		if name == "" {
			return nil, fmt.Errorf("namespace with an empty name")
		}
		out.namespaces[name] = cloneLabels(namespace.Labels)
	}
	for key, pod := range cluster.Pods {
		if pod == nil {
			return nil, fmt.Errorf("pod %q is nil", key)
		}
		compiled, err := compilePod(pod)
		if err != nil {
			return nil, fmt.Errorf("pod %q: %w", key, err)
		}
		qualified := podKey(compiled.namespace, compiled.name)
		if _, exists := out.byQualified[qualified]; exists {
			return nil, fmt.Errorf("duplicate pod %q", qualified)
		}
		out.byQualified[qualified] = compiled
		out.byName[compiled.name] = append(out.byName[compiled.name], compiled)
		for _, addr := range compiled.addrs {
			out.byIP[addr] = append(out.byIP[addr], compiled)
		}
	}
	for _, pods := range out.byName {
		sort.Slice(pods, func(i, j int) bool {
			return podKey(pods[i].namespace, pods[i].name) < podKey(pods[j].namespace, pods[j].name)
		})
	}
	return out, nil
}

func (c *compiledCluster) lookupAddress(addr netip.Addr) *compiledPod {
	pods := c.byIP[addr]
	if len(pods) == 1 {
		return pods[0]
	}
	return nil
}

func compilePod(pod *Pod) (*compiledPod, error) {
	if pod == nil {
		return nil, fmt.Errorf("nil pod")
	}
	if strings.TrimSpace(pod.Name) == "" {
		return nil, fmt.Errorf("pod name must not be empty")
	}
	compiled := &compiledPod{
		namespace: pod.Namespace,
		name:      pod.Name,
		labels:    cloneLabels(pod.Labels),
		ports:     make(map[string]ContainerPort),
	}

	seen := make(map[netip.Addr]struct{})
	for _, raw := range pod.Endpoints() {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		addr, err := netip.ParseAddr(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid pod IP %q: %w", raw, err)
		}
		addr = addr.Unmap()
		if _, exists := seen[addr]; exists {
			continue
		}
		seen[addr] = struct{}{}
		compiled.addrs = append(compiled.addrs, addr)
	}
	sort.Slice(compiled.addrs, func(i, j int) bool {
		return compiled.addrs[i].Compare(compiled.addrs[j]) < 0
	})

	mergePorts := func(ports map[string]ContainerPort) error {
		for name, port := range ports {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("named port has an empty name")
			}
			if err := validatePortNumber(port.ContainerPort); err != nil {
				return fmt.Errorf("named port %q: %w", name, err)
			}
			normalized, err := normalizeProtocol(port.Protocol)
			if err != nil {
				return fmt.Errorf("named port %q: %w", name, err)
			}
			port.Protocol = normalized
			if existing, exists := compiled.ports[name]; exists && existing != port {
				return fmt.Errorf("named port %q is defined more than once with different values", name)
			}
			compiled.ports[name] = port
		}
		return nil
	}
	if err := mergePorts(pod.Ports); err != nil {
		return nil, err
	}
	if err := mergePorts(pod.NamedPorts); err != nil {
		return nil, err
	}
	return compiled, nil
}

func (c *compiledCluster) lookupQualified(namespace, name string) (*compiledPod, bool) {
	pod, ok := c.byQualified[podKey(namespace, name)]
	return pod, ok
}

func (c *compiledCluster) lookupName(name string) (*compiledPod, error) {
	pods := c.byName[name]
	switch len(pods) {
	case 0:
		return nil, fmt.Errorf("pod %q was not found", name)
	case 1:
		return pods[0], nil
	default:
		names := make([]string, 0, len(pods))
		for _, pod := range pods {
			names = append(names, podKey(pod.namespace, pod.name))
		}
		sort.Strings(names)
		return nil, fmt.Errorf("pod name %q is ambiguous; use one of %s", name, strings.Join(names, ", "))
	}
}

package pathsec

import (
	"fmt"
	"sort"
)

// MaxAdversarialAS is the bounded graph size required by the task.
const MaxAdversarialAS = 12

// RelationKind describes a policy edge.
type RelationKind string

const (
	RelationCustomerProvider RelationKind = "customer-provider"
	RelationProviderCustomer RelationKind = "provider-customer"
	RelationPeer             RelationKind = "peer"
	RelationRSClient         RelationKind = "rs-client"
	RelationRS               RelationKind = "rs"
	RelationUnknown          RelationKind = "unknown"
)

// PolicyRelation is a directed customer/provider or flat relation.
type PolicyRelation struct {
	Type        RelationKind `json:"type,omitempty"`
	Kind        RelationKind `json:"kind,omitempty"`
	CustomerAS  uint32       `json:"customerAS,omitempty"`
	CustomerASN uint32       `json:"customerASN,omitempty"`
	ProviderAS  uint32       `json:"providerAS,omitempty"`
	ProviderASN uint32       `json:"providerASN,omitempty"`
	ClientAS    uint32       `json:"clientAS,omitempty"`
	ClientASN   uint32       `json:"clientASN,omitempty"`
	RSASN       uint32       `json:"rsASN,omitempty"`
	AS1         uint32       `json:"as1,omitempty"`
	AS2         uint32       `json:"as2,omitempty"`
	A           uint32       `json:"a,omitempty"`
	B           uint32       `json:"b,omitempty"`
	// ASPAHidden simulates an unpublished ASPA for this customer.
	ASPAHidden bool `json:"aspaHidden,omitempty"`
}

func (r PolicyRelation) kind() RelationKind {
	if r.Kind != "" {
		return normalizeRelationKind(r.Kind)
	}
	return normalizeRelationKind(r.Type)
}

func normalizeRelationKind(kind RelationKind) RelationKind {
	s := string(kind)
	switch s {
	case "", "unknown":
		return RelationUnknown
	case "customer-provider", "customer_provider", "cp", "c2p", "customer->provider":
		return RelationCustomerProvider
	case "provider-customer", "provider_customer", "pc", "p2c":
		return RelationProviderCustomer
	case "peer", "peering", "p2p":
		return RelationPeer
	case "rs-client", "rs_client", "rsclient":
		return RelationRSClient
	case "rs", "route-server", "route_server":
		return RelationRS
	default:
		return RelationKind(s)
	}
}

func (r PolicyRelation) endpoints() (uint32, uint32) {
	a := firstNonZero(r.CustomerAS, r.CustomerASN, r.ClientAS, r.ClientASN, r.AS1, r.A)
	b := firstNonZero(r.ProviderAS, r.ProviderASN, r.RSASN, r.AS2, r.B)
	if a == 0 || b == 0 {
		a = firstNonZero(r.AS1, r.A, r.CustomerAS, r.CustomerASN, r.ClientAS, r.ClientASN)
		b = firstNonZero(r.AS2, r.B, r.ProviderAS, r.ProviderASN, r.RSASN)
	}
	return a, b
}

// GraphNode is an AS in the adversarial topology.
type GraphNode struct {
	ASN         uint32 `json:"asn"`
	AS          uint32 `json:"AS,omitempty"`
	Name        string `json:"name,omitempty"`
	Corrupted   bool   `json:"corrupted,omitempty"`
	Compromised bool   `json:"compromised,omitempty"`
	Malicious   bool   `json:"malicious,omitempty"`
}

// GraphLink is an undirected topology edge.
type GraphLink struct {
	A    uint32 `json:"a,omitempty"`
	B    uint32 `json:"b,omitempty"`
	From uint32 `json:"from,omitempty"`
	To   uint32 `json:"to,omitempty"`
	AS1  uint32 `json:"as1,omitempty"`
	AS2  uint32 `json:"as2,omitempty"`
}

func (l GraphLink) endpoints() (uint32, uint32) {
	return firstNonZero(l.A, l.From, l.AS1), firstNonZero(l.B, l.To, l.AS2)
}

type edgeKeyT struct {
	A uint32
	B uint32
}

func edgeKey(a, b uint32) edgeKeyT {
	if a <= b {
		return edgeKeyT{A: a, B: b}
	}
	return edgeKeyT{A: b, B: a}
}

// AttackGraph is a bounded AS topology with policy and corruption state.
type AttackGraph struct {
	Nodes     map[uint32]GraphNode
	Adjacency map[uint32]map[uint32]bool
	Relations map[edgeKeyT]PolicyRelation
	Corrupted map[uint32]bool

	ROA  *ROATable
	ASPA *ASPATable
	Keys *KeyStore

	// UnpublishedASPs removes derived ASPA records for these customers.
	UnpublishedASPAs map[uint32]bool
}

// Graph is a concise alias for callers expecting that name.
type Graph = AttackGraph

// NewGraph creates an empty adversarial graph.
func NewGraph() *AttackGraph {
	return &AttackGraph{
		Nodes:            make(map[uint32]GraphNode),
		Adjacency:        make(map[uint32]map[uint32]bool),
		Relations:        make(map[edgeKeyT]PolicyRelation),
		Corrupted:        make(map[uint32]bool),
		UnpublishedASPAs: make(map[uint32]bool),
	}
}

// NewGraphFromTopology builds a graph from a declarative topology.
func NewGraphFromTopology(topology Topology) (*AttackGraph, error) {
	graph := NewGraph()
	for _, asn := range topology.ASNs {
		graph.AddAS(asn)
	}
	for _, node := range topology.Nodes {
		graph.AddNode(node)
	}
	for _, asn := range topology.Corrupted {
		graph.Corrupt(asn)
	}
	for _, asn := range topology.Compromised {
		graph.Corrupt(asn)
	}
	for _, asn := range topology.Malicious {
		graph.Corrupt(asn)
	}
	for _, link := range topology.Links {
		a, b := link.endpoints()
		graph.AddLink(a, b)
	}
	for _, edge := range topology.Edges {
		a, b := edge.endpoints()
		graph.AddLink(a, b)
	}
	for _, relation := range topology.Relations {
		if err := graph.AddRelationE(relation); err != nil {
			return nil, err
		}
	}
	if err := graph.CheckBounds(); err != nil {
		return nil, err
	}
	return graph, nil
}

// Topology is a declarative graph fixture.
type Topology struct {
	ASNs        []uint32         `json:"asns,omitempty"`
	Nodes       []GraphNode      `json:"nodes,omitempty"`
	Links       []GraphLink      `json:"links,omitempty"`
	Edges       []GraphLink      `json:"edges,omitempty"`
	Relations   []PolicyRelation `json:"relations,omitempty"`
	Corrupted   []uint32         `json:"corrupted,omitempty"`
	Compromised []uint32         `json:"compromised,omitempty"`
	Malicious   []uint32         `json:"malicious,omitempty"`
}

// AddAS inserts an AS node.
func (g *AttackGraph) AddAS(asn uint32, corrupted ...bool) *AttackGraph {
	node := GraphNode{ASN: asn}
	if len(corrupted) > 0 {
		node.Corrupted = corrupted[0]
	}
	g.AddNode(node)
	return g
}

// AddNode inserts a rich node.
func (g *AttackGraph) AddNode(node GraphNode) *AttackGraph {
	node.ASN = firstNonZero(node.ASN, node.AS)
	node.AS = node.ASN
	if g.Nodes == nil {
		g.Nodes = make(map[uint32]GraphNode)
	}
	if g.Adjacency == nil {
		g.Adjacency = make(map[uint32]map[uint32]bool)
	}
	if g.Relations == nil {
		g.Relations = make(map[edgeKeyT]PolicyRelation)
	}
	if g.Corrupted == nil {
		g.Corrupted = make(map[uint32]bool)
	}
	g.Nodes[node.ASN] = node
	if node.Corrupted || node.Compromised || node.Malicious {
		g.Corrupted[node.ASN] = true
	}
	if g.Adjacency[node.ASN] == nil {
		g.Adjacency[node.ASN] = make(map[uint32]bool)
	}
	return g
}

// AddLink inserts an undirected topology edge.
func (g *AttackGraph) AddLink(a, b uint32) *AttackGraph {
	g.AddAS(a)
	g.AddAS(b)
	g.Adjacency[a][b] = true
	g.Adjacency[b][a] = true
	return g
}

// AddEdge is an alias for AddLink.
func (g *AttackGraph) AddEdge(a, b uint32) *AttackGraph { return g.AddLink(a, b) }

// AddRelationE inserts a policy relation and corresponding link.
func (g *AttackGraph) AddRelationE(relation PolicyRelation) error {
	kind := relation.kind()
	a, b := relation.endpoints()
	if a == 0 || b == 0 {
		return fmt.Errorf("%w: relation missing endpoints", ErrInvalidASN)
	}
	if kind == RelationUnknown {
		return fmt.Errorf("pathsec: unknown policy relation type %q", relation.Type)
	}
	g.AddLink(a, b)

	stored := relation
	stored.Type = kind
	stored.Kind = kind
	switch kind {
	case RelationCustomerProvider:
		stored.CustomerAS = a
		stored.ProviderAS = b
	case RelationProviderCustomer:
		stored.ProviderAS = a
		stored.CustomerAS = b
		stored.Type = RelationCustomerProvider
		stored.Kind = RelationCustomerProvider
	case RelationRSClient:
		stored.ClientAS = a
		stored.RSASN = b
	case RelationRS:
		// Relation{Type:"rs", AS1:rs, AS2:client} can be normalized by callers;
		// endpoints are stored as-is and orientation helpers understand it.
		stored.AS1 = a
		stored.AS2 = b
	case RelationPeer:
		stored.AS1 = a
		stored.AS2 = b
	}
	g.Relations[edgeKey(a, b)] = stored
	return nil
}

// AddRelation panics on invalid relations; it is fixture-friendly.
func (g *AttackGraph) AddRelation(relation PolicyRelation) *AttackGraph {
	if err := g.AddRelationE(relation); err != nil {
		panic(err)
	}
	return g
}

// AddCustomerProvider records customer -> provider.
func (g *AttackGraph) AddCustomerProvider(customer, provider uint32) *AttackGraph {
	return g.AddRelation(PolicyRelation{Type: RelationCustomerProvider, CustomerAS: customer, ProviderAS: provider})
}

// AddProviderCustomer records provider -> customer.
func (g *AttackGraph) AddProviderCustomer(provider, customer uint32) *AttackGraph {
	return g.AddCustomerProvider(customer, provider)
}

// AddPeer records a symmetric peering edge.
func (g *AttackGraph) AddPeer(a, b uint32) *AttackGraph {
	return g.AddRelation(PolicyRelation{Type: RelationPeer, AS1: a, AS2: b})
}

// AddRSClient records client -> route server.
func (g *AttackGraph) AddRSClient(client, routeServer uint32) *AttackGraph {
	return g.AddRelation(PolicyRelation{Type: RelationRSClient, ClientAS: client, RSASN: routeServer})
}

// Corrupt marks ASes as partially corrupted/compromised.
func (g *AttackGraph) Corrupt(asns ...uint32) *AttackGraph {
	for _, asn := range asns {
		g.AddAS(asn)
		g.Corrupted[asn] = true
		if node, ok := g.Nodes[asn]; ok {
			node.Corrupted = true
			node.Compromised = true
			node.Malicious = true
			g.Nodes[asn] = node
		}
	}
	return g
}

// Compromise is an alias for Corrupt.
func (g *AttackGraph) Compromise(asns ...uint32) *AttackGraph { return g.Corrupt(asns...) }

// MarkMalicious is an alias for Corrupt.
func (g *AttackGraph) MarkMalicious(asns ...uint32) *AttackGraph { return g.Corrupt(asns...) }

// PublishMissingASPA marks a customer as unpublished in derived ASPA.
func (g *AttackGraph) PublishMissingASPA(customer uint32) *AttackGraph {
	if g.UnpublishedASPAs == nil {
		g.UnpublishedASPAs = make(map[uint32]bool)
	}
	g.UnpublishedASPAs[customer] = true
	return g
}

// CheckBounds enforces the <=12 AS search bound.
func (g *AttackGraph) CheckBounds() error {
	if g == nil {
		return fmt.Errorf("pathsec: nil graph")
	}
	if len(g.Nodes) > MaxAdversarialAS {
		return fmt.Errorf("%w: got %d AS nodes", ErrGraphLimit, len(g.Nodes))
	}
	return nil
}

// ASNs returns sorted AS numbers.
func (g *AttackGraph) ASNs() []uint32 {
	out := make([]uint32, 0, len(g.Nodes))
	for asn := range g.Nodes {
		out = append(out, asn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Neighbors returns sorted topology neighbors.
func (g *AttackGraph) Neighbors(asn uint32) []uint32 {
	var out []uint32
	for neighbor := range g.Adjacency[asn] {
		out = append(out, neighbor)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Relation returns the policy relation between two adjacent ASes.
func (g *AttackGraph) Relation(a, b uint32) (PolicyRelation, bool) {
	rel, ok := g.Relations[edgeKey(a, b)]
	return rel, ok
}

// EdgeOrientation classifies traversal a -> b in traffic direction.
// U means customer->provider, D means provider->customer, F means flat.
func (g *AttackGraph) EdgeOrientation(a, b uint32) byte {
	rel, ok := g.Relation(a, b)
	if !ok {
		return '?'
	}
	switch rel.kind() {
	case RelationCustomerProvider:
		if rel.CustomerAS == a && rel.ProviderAS == b {
			return 'U'
		}
		if rel.CustomerAS == b && rel.ProviderAS == a {
			return 'D'
		}
	case RelationProviderCustomer:
		return '?'
	case RelationPeer:
		return 'F'
	case RelationRSClient:
		// Client -> RS is operationally flat for route distribution; treating
		// it as F keeps it out of the customer/provider valley ladder.
		return 'F'
	case RelationRS:
		return 'F'
	}
	return '?'
}

// OrientationSequence returns edge orientations for a node path.
func (g *AttackGraph) OrientationSequence(nodePath []uint32) []byte {
	out := make([]byte, 0, maxInt(0, len(nodePath)-1))
	for i := 0; i+1 < len(nodePath); i++ {
		out = append(out, g.EdgeOrientation(nodePath[i], nodePath[i+1]))
	}
	return out
}

// IsValleyFree reports whether nodePath follows U* F? D*.
// Unknown edges are allowed when allowUnknown is true and represented by '?';
// they make the policy result non-authoritative rather than a proven leak.
func IsValleyFree(orientations []byte, allowUnknown bool) bool {
	phase := 0 // 0=uphill, 1=flat seen, 2=downhill
	for _, edge := range orientations {
		switch edge {
		case 'U':
			if phase >= 1 {
				return false
			}
		case 'F':
			if phase >= 1 {
				return false
			}
			phase = 1
		case 'D':
			phase = 2
		case '?':
			if !allowUnknown {
				return false
			}
			// Unknown edges do not prove a valley, but do not change phase.
		default:
			return false
		}
	}
	return true
}

// IsRouteLeakPath applies the observer role edge and valley-free policy.
// nodePath must include observer as nodePath[0].
func (g *AttackGraph) IsRouteLeakPath(nodePath []uint32, side Role) bool {
	if len(nodePath) < 2 {
		return false
	}
	orientations := g.OrientationSequence(nodePath)
	if len(orientations) > 0 && orientations[0] == '?' {
		orientations[0] = roleEdgeOrientation(side)
	}
	return !IsValleyFree(orientations, false)
}

func roleEdgeOrientation(side Role) byte {
	switch normalizeRole(side) {
	case RoleCustomer, RoleRS:
		// Local provider/RS -> customer/RS-client is downhill.
		return 'D'
	case RoleProvider:
		// Local customer -> provider is uphill.
		return 'U'
	case RolePeer, RoleRSClient:
		return 'F'
	default:
		return '?'
	}
}

// EffectiveASPA returns the explicitly attached table or derives a full
// policy ASPA table.
func (g *AttackGraph) EffectiveASPA() *ASPATable {
	if g.ASPA != nil {
		return g.ASPA
	}
	return g.DeriveASPA(true)
}

// DeriveASPA builds ASPA records from customer-provider policy relations.
// If publishEmptyForStubs is true, every node with no provider gets an
// explicit empty ASPA unless it is in UnpublishedASPAs.
func (g *AttackGraph) DeriveASPA(publishEmptyForStubs bool) *ASPATable {
	table := NewASPATable()
	providers := make(map[uint32]map[uint32]struct{})
	hidden := make(map[uint32]bool)
	for _, rel := range g.Relations {
		if rel.kind() != RelationCustomerProvider {
			continue
		}
		if rel.ASPAHidden || g.UnpublishedASPAs[rel.CustomerAS] {
			hidden[rel.CustomerAS] = true
			continue
		}
		if providers[rel.CustomerAS] == nil {
			providers[rel.CustomerAS] = make(map[uint32]struct{})
		}
		providers[rel.CustomerAS][rel.ProviderAS] = struct{}{}
	}
	for customer := range g.Nodes {
		if g.UnpublishedASPAs[customer] || hidden[customer] {
			continue
		}
		var list []uint32
		for provider := range providers[customer] {
			list = append(list, provider)
		}
		sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
		if len(list) > 0 {
			_ = table.Publish(customer, list...)
		} else if publishEmptyForStubs {
			_ = table.PublishEmpty(customer)
		}
	}
	return table
}

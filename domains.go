package netsec

import "sort"

// dsu is a disjoint-set union used to merge organizations into control
// domains. Control edges are treated as undirected connections: any two
// organizations connected by a directed control chain in either direction
// share one controlling party, so they cannot be counted as independent.
// This correctly collapses chains, cycles and diamond-shaped hierarchies.
type dsu struct {
	parent map[string]string
	rank   map[string]int
}

func newDSU() *dsu {
	return &dsu{
		parent: map[string]string{},
		rank:   map[string]int{},
	}
}

func (d *dsu) add(id string) {
	if _, ok := d.parent[id]; !ok {
		d.parent[id] = id
		d.rank[id] = 0
	}
}

func (d *dsu) find(id string) string {
	root := id
	for d.parent[root] != root {
		root = d.parent[root]
	}
	for d.parent[id] != id {
		next := d.parent[id]
		d.parent[id] = root
		id = next
	}
	return root
}

func (d *dsu) union(a, b string) {
	d.add(a)
	d.add(b)
	ra, rb := d.find(a), d.find(b)
	if ra == rb {
		return
	}
	switch {
	case d.rank[ra] < d.rank[rb]:
		d.parent[ra] = rb
	case d.rank[ra] > d.rank[rb]:
		d.parent[rb] = ra
	default:
		d.parent[rb] = ra
		d.rank[ra]++
	}
}

// buildDomains merges organizations into control domains and returns a map
// from organization ID to a stable domain ID (the smallest organization ID in
// the connected component).
func buildDomains(orgs map[string]Organization, edges []ControlEdge) map[string]string {
	set := newDSU()
	for id := range orgs {
		set.add(id)
	}
	for _, edge := range edges {
		set.union(edge.From, edge.To)
	}

	members := map[string][]string{}
	for id := range orgs {
		root := set.find(id)
		members[root] = append(members[root], id)
	}

	domainOf := make(map[string]string, len(orgs))
	for _, group := range members {
		names := append([]string(nil), group...)
		sort.Strings(names)
		domainID := names[0]
		for _, id := range group {
			domainOf[id] = domainID
		}
	}
	return domainOf
}

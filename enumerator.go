package netsec

import "strconv"

// ConcreteReach is one packet observed by exhaustive enumeration at a node,
// together with the first (shortest) concrete path that delivered it and the
// original ingress packet that the journey started from.
type ConcreteReach struct {
	Packet Packet
	Path   []int
	Origin Packet
}

// EnumerationReport is the exhaustive small-domain ground truth.
type EnumerationReport struct {
	Domain          *NarrowedDomain
	PacketsTested   int
	Reachable       map[int][]ConcreteReach
	FilteredPackets int
	OverflowPackets int
}

type bfsItem struct {
	node   int
	packet Packet
	path   []int
	origin Packet
}

// Enumerate performs an exhaustive concrete search over domain: every packet
// satisfying ingress is injected at entry and propagated through the graph
// with the exact concrete semantics (ordered filter, then translation with
// overflow checks). Each concrete packet state is visited at most once per
// node, which makes cycles terminate without approximation because the
// domain is finite and delta-closed.
func Enumerate(g *Graph, entry int, ingress []Constraint, domain *NarrowedDomain) (*EnumerationReport, error) {
	if _, ok := g.Nodes[entry]; !ok {
		return nil, errUnknownNode(entry)
	}
	report := &EnumerationReport{
		Domain:    domain,
		Reachable: make(map[int][]ConcreteReach),
	}
	seen := make(map[int]map[uint64]struct{})
	queue := make([]bfsItem, 0)

	for _, p := range domain.packetsMatching(ingress) {
		report.PacketsTested++
		seed := bfsItem{node: entry, packet: p, path: []int{entry}, origin: p}
		markSeen(seen, entry, p)
		report.Reachable[entry] = append(report.Reachable[entry], ConcreteReach{
			Packet: p,
			Path:   []int{entry},
			Origin: p,
		})
		queue = append(queue, seed)
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		node := g.Nodes[item.node]
		next, outcome, _ := stepPacket(node, item.packet)
		if outcome == OutcomeFiltered {
			report.FilteredPackets++
			continue
		}
		if outcome == OutcomeOverflow {
			report.OverflowPackets++
			continue
		}
		for _, dst := range g.Edges[item.node] {
			if isSeen(seen, dst, next) {
				continue
			}
			markSeen(seen, dst, next)
			path := append(clonePath(item.path), dst)
			report.Reachable[dst] = append(report.Reachable[dst], ConcreteReach{
				Packet: next,
				Path:   clonePath(path),
				Origin: item.origin,
			})
			queue = append(queue, bfsItem{
				node:   dst,
				packet: next,
				path:   path,
				origin: item.origin,
			})
		}
	}
	return report, nil
}

func isSeen(seen map[int]map[uint64]struct{}, node int, p Packet) bool {
	if at, ok := seen[node]; ok {
		_, dup := at[packetKey(p)]
		return dup
	}
	return false
}

func markSeen(seen map[int]map[uint64]struct{}, node int, p Packet) {
	at, ok := seen[node]
	if !ok {
		at = make(map[uint64]struct{})
		seen[node] = at
	}
	at[packetKey(p)] = struct{}{}
}

func packetKey(p Packet) uint64 {
	return uint64(p.Src)<<32 | uint64(p.Dst)
}

func errUnknownNode(id int) error {
	return &nodeError{id: id}
}

type nodeError struct{ id int }

func (e *nodeError) Error() string {
	return ErrUnknownNode.Error() + ": " + strconv.Itoa(e.id)
}

func (e *nodeError) Unwrap() error { return ErrUnknownNode }

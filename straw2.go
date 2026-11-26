package crush

import (
	"math"
	"strings"
)

const maxWalkDepth = 64

// straw2Winner performs one Straw2 bucket draw. The hash tuple contains the
// PG, rule step, slot/sequence, retry coordinate, walk depth and bucket ID.
func (m *Mapper) straw2Winner(bucket *Node, context ...uint64) (*Node, string) {
	children := bucket.configuredChildren()
	if bucket == nil || len(children) == 0 {
		return nil, "no_candidate"
	}

	var best *Node
	bestScore := math.Inf(-1)
	positive := 0
	zeroOrDrained := 0

	for _, child := range children {
		if child == nil {
			continue
		}
		weight := m.nodeWeight(child)
		if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			zeroOrDrained++
			continue
		}
		positive++

		parts := make([]uint64, 0, len(context)+2)
		parts = append(parts, context...)
		parts = append(parts, uint64(bucket.ID), uint64(child.ID))
		draw := m.hashTuple(parts...)
		u := uniformUnit(draw)
		score := math.Log(u) / weight

		if best == nil || score > bestScore || (score == bestScore && child.ID < best.ID) {
			bestScore = score
			best = child
		}
	}

	if best == nil {
		if zeroOrDrained > 0 && positive == 0 {
			return nil, "zero_weight"
		}
		return nil, "no_positive_child"
	}
	return best, ""
}

func uniformUnit(h uint64) float64 {
	const scale = float64(uint64(1) << 53)
	x := h >> 11
	return (float64(x) + 0.5) / scale
}

// chooseDomain follows Straw2 draws until domainType is reached. An empty or
// "osd" domain descends all the way to a leaf.
func (m *Mapper) chooseDomain(start *Node, domainType string, context ...uint64) (*Node, []*Node, string) {
	if start == nil {
		return nil, nil, "no_domain"
	}

	domainType = normalizeDomain(domainType, false)
	path := []*Node{start}
	current := start

	for depth := 0; depth < maxWalkDepth; depth++ {
		if current == nil {
			return nil, path, "no_domain"
		}
		if domainMatches(current, domainType) {
			return current, path, ""
		}
		if current.IsLeaf() {
			if isOSDDomain(domainType) || domainType == "" {
				return current, path, ""
			}
			return current, path, "not_found"
		}
		if !m.nodeUp(current) {
			return current, path, "down"
		}

		drawContext := append(append([]uint64{}, context...), uint64(depth))
		child, reason := m.straw2Winner(current, drawContext...)
		if reason != "" {
			return current, path, reason
		}
		path = append(path, child)
		current = child
	}
	return current, path, "depth_limit"
}

// walkToLeaf descends from an already selected domain bucket to one OSD leaf.
func (m *Mapper) walkToLeaf(domain *Node, context ...uint64) (*Node, []*Node, string) {
	if domain == nil {
		return nil, nil, "no_leaf"
	}
	path := []*Node{domain}
	current := domain

	for depth := 0; depth < maxWalkDepth; depth++ {
		if current == nil {
			return nil, path, "no_leaf"
		}
		if current.IsLeaf() {
			if !m.nodeUp(current) {
				return current, path, "down"
			}
			if m.nodeWeight(current) <= 0 {
				return current, path, "zero_weight"
			}
			return current, path, ""
		}
		if !m.nodeUp(current) {
			return current, path, "down"
		}

		drawContext := append(append([]uint64{}, context...), uint64(depth))
		child, reason := m.straw2Winner(current, drawContext...)
		if reason != "" {
			return current, path, reason
		}
		path = append(path, child)
		current = child
	}
	return current, path, "depth_limit"
}

func domainMatches(node *Node, domainType string) bool {
	if node == nil {
		return false
	}
	if domainType == "" {
		return true
	}
	if isOSDDomain(domainType) {
		return node.IsLeaf() || strings.EqualFold(node.NodeType(), "osd")
	}
	return strings.EqualFold(node.NodeType(), domainType)
}

// Straw2Score exposes the raw ln(u)/weight score for tests and diagnostics.
func Straw2Score(id uint64, weight float64, seed uint64, hash Hash64Func) float64 {
	if hash == nil {
		hash = DefaultHash
	}
	u := uniformUnit(hash(seed, id))
	if weight <= 0 {
		return math.Inf(-1)
	}
	return math.Log(u) / weight
}

// Straw2Select performs a one-shot Straw2 draw over children using the default
// tuple hash adapter. It is a low-level building block; Mapper supplies the
// PG/step/retry context automatically.
func Straw2Select(children []*Node, seed uint64, hasher any) (*Node, error) {
	h, err := adaptHasher(hasher)
	if err != nil {
		return nil, err
	}
	m := &Mapper{hash: h}
	winner, _ := m.straw2Winner(&Node{ID: 0, Type: "bucket", Children: children}, seed)
	return winner, nil
}

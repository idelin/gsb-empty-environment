package upgradeplan

import (
	"fmt"
	"sort"
	"strings"
)

// Session is a count-only runtime cursor over a Plan. It never accepts device
// identities or signature details from the server response.
type Session struct {
	plan *Plan
	node *PlanNode
}

// NewSession starts at the plan root.
func NewSession(plan *Plan) (*Session, error) {
	if plan == nil || plan.Root == nil {
		return nil, fmt.Errorf("upgradeplan: cannot start session from nil plan")
	}
	return &Session{plan: plan, node: plan.Root}, nil
}

// Current returns the broadcast that should currently be sent.
func (s *Session) Current() (*Broadcast, bool) {
	if s == nil || s.node == nil || s.node.Terminal || s.node.Broadcast == nil {
		return nil, false
	}
	return s.node.Broadcast, true
}

// Done reports whether the session reached a terminal branch.
func (s *Session) Done() bool {
	return s == nil || s.node == nil || s.node.Terminal
}

// Observe feeds the only allowed runtime observation, the success count, and
// advances to the one broadcast selected for that count branch.
func (s *Session) Observe(successCount int) (*Broadcast, error) {
	if s == nil || s.node == nil {
		return nil, fmt.Errorf("upgradeplan: session is not initialized")
	}
	if s.node.Terminal {
		return nil, fmt.Errorf("upgradeplan: session already terminal")
	}
	child, ok := s.node.Branches[successCount]
	if !ok {
		counts := make([]int, 0, len(s.node.Branches))
		for count := range s.node.Branches {
			counts = append(counts, count)
		}
		sort.Ints(counts)
		parts := make([]string, len(counts))
		for i, count := range counts {
			parts[i] = fmt.Sprintf("%d", count)
		}
		return nil, fmt.Errorf("upgradeplan: no plan branch for count %d (known counts: %s)", successCount, strings.Join(parts, ","))
	}
	s.node = child
	if child.Terminal {
		return nil, nil
	}
	return child.Broadcast, nil
}

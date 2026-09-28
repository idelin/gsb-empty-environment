package flowlb

import "sort"

// Path is one candidate egress link for flow assignment.
type Path struct {
	ID     string
	Weight int // relative capacity weight; 0 means "no new flows"
	Rate   int // link service capacity in packets per tick
}

// Assigner maps stable flow IDs to paths using weighted rendezvous hashing.
// Assignments change only when the path set changes, and then only for the
// flows that must move (removed path, or a better-scoring new path).
type Assigner struct {
	paths   map[string]Path
	current map[string]string // flowID -> pathID
}

func NewAssigner() *Assigner {
	return &Assigner{
		paths:   make(map[string]Path),
		current: make(map[string]string),
	}
}

// Paths returns the known paths sorted by ID for deterministic output.
func (a *Assigner) Paths() []Path {
	out := make([]Path, 0, len(a.paths))
	for _, p := range a.paths {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TotalFlows returns the number of flows with a recorded assignment.
func (a *Assigner) TotalFlows() int { return len(a.current) }

// best returns the highest-scoring eligible path for flowID.
func (a *Assigner) best(flowID string) (string, bool) {
	bestID := ""
	bestScore := score(flowID, Path{}) // -Inf
	found := false
	for _, p := range a.paths {
		if p.Weight <= 0 {
			continue
		}
		if s := score(flowID, p); s > bestScore {
			bestScore, bestID, found = s, p.ID, true
		}
	}
	return bestID, found
}

// Assign returns the stable path for flowID, creating the mapping on first
// use. ok is false when no path with positive weight exists.
func (a *Assigner) Assign(flowID string) (pathID string, ok bool) {
	if id, seen := a.current[flowID]; seen {
		return id, true
	}
	id, ok := a.best(flowID)
	if !ok {
		return "", false
	}
	a.current[flowID] = id
	return id, true
}

// AddPath registers (or replaces) a path and migrates only the flows whose
// best path is now the added one. It returns how many existing flows moved.
func (a *Assigner) AddPath(p Path) (moved int) {
	a.paths[p.ID] = p
	if p.Weight <= 0 {
		return 0
	}
	for flowID, cur := range a.current {
		curScore := score(flowID, a.paths[cur])
		if score(flowID, p) > curScore {
			a.current[flowID] = p.ID
			moved++
		}
	}
	return moved
}

// RemovePath deletes a path. Only flows currently on that path are
// re-assigned; flows that cannot be placed (no positive-weight path left)
// are counted as dropped.
func (a *Assigner) RemovePath(id string) (migrated, dropped int) {
	if _, ok := a.paths[id]; !ok {
		return 0, 0
	}
	delete(a.paths, id)
	for flowID, cur := range a.current {
		if cur != id {
			continue
		}
		if next, ok := a.best(flowID); ok {
			a.current[flowID] = next
			migrated++
		} else {
			delete(a.current, flowID)
			dropped++
		}
	}
	return migrated, dropped
}

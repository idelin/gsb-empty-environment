package migrate

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Op renames one node from its original name to a temporary name
// (phase 1) and then to its final name (phase 2).
type Op struct {
	NodeID int    `json:"node_id"`
	From   string `json:"from"`
	Temp   string `json:"temp"`
	Final  string `json:"final"`
}

// Plan is the full, precomputed rename mapping for a tree. Because the
// entire mapping is computed before anything is renamed, collisions are
// resolved up front and the two-phase apply never overwrites anything.
type Plan struct {
	Ops []Op `json:"ops"`
}

// BuildPlan computes the normalized target of every node in t and
// returns a rename plan. Directories are processed before their
// descendants, so a renamed directory shifts the target paths of its
// whole subtree. When several nodes normalize to the same target path,
// the group is sorted by original path: the first node keeps the name
// and the rest receive stable numeric suffixes ("~2", "~3", ...).
func BuildPlan(t *Tree, rules Rules) *Plan {
	finalName := map[int]string{t.Root.ID: t.Root.Name}
	finalPath := map[int]string{t.Root.ID: ""}

	// Group nodes by depth so parents are resolved before children.
	byDepth := map[int][]*Node{}
	maxDepth := 0
	for _, n := range t.Nodes() {
		d := depth(n)
		byDepth[d] = append(byDepth[d], n)
		if d > maxDepth {
			maxDepth = d
		}
	}

	for d := 1; d <= maxDepth; d++ {
		nodes := byDepth[d]
		// Bucket the level's nodes by their would-be target path.
		buckets := map[string][]*Node{}
		for _, n := range nodes {
			name := rules.Normalize(n.Name)
			finalName[n.ID] = name
			p := finalPath[n.Parent.ID] + "/" + name
			buckets[p] = append(buckets[p], n)
		}
		for _, group := range buckets {
			if len(group) > 1 {
				sort.Slice(group, func(i, j int) bool {
					return group[i].Path() < group[j].Path()
				})
				for i, n := range group[1:] {
					finalName[n.ID] = addSuffix(finalName[n.ID], i+2, n.Dir)
				}
			}
			for _, n := range group {
				finalPath[n.ID] = finalPath[n.Parent.ID] + "/" + finalName[n.ID]
			}
		}
	}

	plan := &Plan{}
	for _, n := range t.Nodes() {
		if finalName[n.ID] == n.Name {
			continue
		}
		plan.Ops = append(plan.Ops, Op{
			NodeID: n.ID,
			From:   n.Name,
			Temp:   tempName(n, finalName),
			Final:  finalName[n.ID],
		})
	}
	sort.Slice(plan.Ops, func(i, j int) bool { return plan.Ops[i].NodeID < plan.Ops[j].NodeID })
	return plan
}

func depth(n *Node) int {
	d := 0
	for cur := n; cur.Parent != nil; cur = cur.Parent {
		d++
	}
	return d
}

// addSuffix appends a stable "~n" suffix to a name, inserting it before
// the extension of a file so "a.txt" becomes "a~2.txt". Directories and
// extension-less names take the suffix at the end.
func addSuffix(name string, n int, dir bool) string {
	suffix := fmt.Sprintf("~%d", n)
	if dir {
		return name + suffix
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i] + suffix + name[i:]
	}
	return name + suffix
}

// tempName derives a per-node temporary name. It must not collide with
// any original or final sibling name, nor with another temp name; the
// reserved prefix makes that unlikely, and collisions are bumped with
// a trailing "_" until the name is free.
func tempName(n *Node, finalName map[int]string) string {
	base := fmt.Sprintf(".migrate-tmp-%d", n.ID)
	taken := map[string]bool{}
	for _, sib := range n.Parent.Children {
		taken[sib.Name] = true
		taken[finalName[sib.ID]] = true
	}
	name := base
	for taken[name] {
		name += "_"
	}
	return name
}

// Apply runs both phases: every node moves to its temporary name, then
// every node moves to its final name. Phase 1 vacates all original
// names, so phase 2 can never collide with a not-yet-renamed node
// (which is what makes name swaps safe).
func (p *Plan) Apply(t *Tree) error {
	if err := p.ApplyPhase1(t); err != nil {
		return err
	}
	return p.ApplyPhase2(t)
}

// ApplyPhase1 renames every planned node to its temporary name.
func (p *Plan) ApplyPhase1(t *Tree) error {
	for _, op := range p.Ops {
		if err := t.Rename(t.Get(op.NodeID), op.Temp); err != nil {
			return err
		}
	}
	return nil
}

// ApplyPhase2 renames every node from its temporary to its final name.
func (p *Plan) ApplyPhase2(t *Tree) error {
	for _, op := range p.Ops {
		if err := t.Rename(t.Get(op.NodeID), op.Final); err != nil {
			return err
		}
	}
	return nil
}

// Recover finishes a plan that was interrupted at any point. It first
// validates every node's state, then re-runs the two phases: nodes
// still at their original name move to their temporary name, and nodes
// at their temporary name move to their final name. Keeping the two
// phases separate matters even here: collapsing them into one pass
// would reintroduce overwrite collisions for name swaps. Recover is
// idempotent and may itself be interrupted and re-run.
func (p *Plan) Recover(t *Tree) error {
	for _, op := range p.Ops {
		n := t.Get(op.NodeID)
		switch n.Name {
		case op.From, op.Temp, op.Final:
			// Recognized state.
		default:
			return fmt.Errorf("migrate: node %d in unexpected state %q (plan: %q -> %q -> %q)",
				op.NodeID, n.Name, op.From, op.Temp, op.Final)
		}
	}
	for _, op := range p.Ops {
		n := t.Get(op.NodeID)
		if n.Name == op.From {
			if err := t.Rename(n, op.Temp); err != nil {
				return err
			}
		}
	}
	for _, op := range p.Ops {
		n := t.Get(op.NodeID)
		if n.Name == op.Temp {
			if err := t.Rename(n, op.Final); err != nil {
				return err
			}
		}
	}
	return nil
}

// Save serializes the plan so an interrupted migration can be resumed
// by a later process.
func (p *Plan) Save(w io.Writer) error {
	return json.NewEncoder(w).Encode(p)
}

// LoadPlan reads a plan previously written by Save.
func LoadPlan(r io.Reader) (*Plan, error) {
	var p Plan
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

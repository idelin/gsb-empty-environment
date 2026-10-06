package main

// Leaf is a terminal item displayed directly under a group.
type Leaf struct {
	Name    string
	Penalty int // paid when the leaf is exposed but is not a target
}

// Group is a collapsible node of the tree view.
type Group struct {
	Name         string
	Parent       int // -1 for the root
	ExpandCost   int
	CollapseCost int
	Leaves       []Leaf
	Blocks       []int // shared data blocks loaded while the group is expanded
}

// Target is a node that must become visible: a whole group (Leaf == -1)
// or one specific leaf (Leaf >= 0) under Group.
type Target struct {
	Group int
	Leaf  int
}

// Instance bundles the tree, the targets and the shared block sizes.
type Instance struct {
	Groups     []Group
	Targets    []Target
	BlockSizes []int
}

// ancestorsOf returns the chain of ancestor groups of g, bottom-up.
func (inst *Instance) ancestorsOf(g int) []int {
	var out []int
	for p := inst.Groups[g].Parent; p != -1; p = inst.Groups[p].Parent {
		out = append(out, p)
	}
	return out
}

// isTargetLeaf reports whether leaf l of group g is a target.
func (inst *Instance) isTargetLeaf(g, l int) bool {
	for _, t := range inst.Targets {
		if t.Group == g && t.Leaf == l {
			return true
		}
	}
	return false
}

// closedMask reports whether the expanded set is ancestor-closed:
// every expanded group has its parent expanded too.
func (inst *Instance) closedMask(mask uint64) bool {
	for g := range inst.Groups {
		if mask&(1<<uint(g)) == 0 {
			continue
		}
		if p := inst.Groups[g].Parent; p != -1 && mask&(1<<uint(p)) == 0 {
			return false
		}
	}
	return true
}

// visible reports whether target t is visible under the expanded set:
// every ancestor group must be expanded, plus the parent group itself
// when the target is a leaf.
func (inst *Instance) visible(mask uint64, t Target) bool {
	if t.Leaf >= 0 && mask&(1<<uint(t.Group)) == 0 {
		return false
	}
	for _, a := range inst.ancestorsOf(t.Group) {
		if mask&(1<<uint(a)) == 0 {
			return false
		}
	}
	return true
}

// allVisible reports whether every target is visible under mask.
func (inst *Instance) allVisible(mask uint64) bool {
	for _, t := range inst.Targets {
		if !inst.visible(mask, t) {
			return false
		}
	}
	return true
}

// memoryOf computes live memory for an expanded set: a shared block
// counts once while at least one expanded group references it.
func (inst *Instance) memoryOf(mask uint64) int {
	rc := make([]int, len(inst.BlockSizes))
	for g := range inst.Groups {
		if mask&(1<<uint(g)) == 0 {
			continue
		}
		for _, b := range inst.Groups[g].Blocks {
			rc[b]++
		}
	}
	total := 0
	for b, c := range rc {
		if c > 0 {
			total += inst.BlockSizes[b]
		}
	}
	return total
}

// refCounts returns the per-block reference counts for an expanded set.
func (inst *Instance) refCounts(mask uint64) []int {
	rc := make([]int, len(inst.BlockSizes))
	for g := range inst.Groups {
		if mask&(1<<uint(g)) == 0 {
			continue
		}
		for _, b := range inst.Groups[g].Blocks {
			rc[b]++
		}
	}
	return rc
}

// demoInstance builds the sample tree used by main and the tests.
func demoInstance() *Instance {
	return &Instance{
		Groups: []Group{
			{Name: "root", Parent: -1, ExpandCost: 3, CollapseCost: 1,
				Leaves: []Leaf{{Name: "README", Penalty: 2}}, Blocks: []int{0}},
			{Name: "src", Parent: 0, ExpandCost: 4, CollapseCost: 1,
				Leaves: []Leaf{{Name: "lib.go", Penalty: 1}, {Name: "util.go", Penalty: 1}},
				Blocks: []int{0, 1}},
			{Name: "docs", Parent: 0, ExpandCost: 2, CollapseCost: 1,
				Leaves: []Leaf{{Name: "guide", Penalty: 3}}, Blocks: []int{2}},
			{Name: "pkg", Parent: 1, ExpandCost: 5, CollapseCost: 2,
				Leaves: []Leaf{{Name: "api.go", Penalty: 2}}, Blocks: []int{1, 3}},
			{Name: "cmd", Parent: 1, ExpandCost: 3, CollapseCost: 1,
				Leaves: []Leaf{{Name: "main.go", Penalty: 1}}, Blocks: []int{3}},
			{Name: "api", Parent: 3, ExpandCost: 6, CollapseCost: 2,
				Leaves: []Leaf{{Name: "handler.go", Penalty: 2}}, Blocks: []int{4}},
			{Name: "util", Parent: 3, ExpandCost: 2, CollapseCost: 1,
				Leaves: []Leaf{{Name: "conv.go", Penalty: 1}}, Blocks: []int{1}},
			{Name: "img", Parent: 2, ExpandCost: 4, CollapseCost: 1,
				Leaves: []Leaf{{Name: "logo", Penalty: 5}}, Blocks: []int{5}},
		},
		Targets: []Target{
			{Group: 4, Leaf: 0},  // cmd/main.go
			{Group: 5, Leaf: 0},  // api/handler.go
			{Group: 7, Leaf: -1}, // the img group itself
		},
		BlockSizes: []int{8, 5, 4, 6, 10, 5},
	}
}

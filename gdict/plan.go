package gdict

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// MappedEntry is one independently reviewable old-code -> new-code row.
type MappedEntry struct {
	Partition string `json:"partition"`
	OldCode   uint64 `json:"old_code"`
	Key       Key    `json:"key"`
	NewCode   uint64 `json:"new_code"`
}

// PartitionPlan contains the translation pages of one partition.
type PartitionPlan struct {
	Name    string        `json:"name"`
	Pages   []Page        `json:"pages"`
	Entries []MappedEntry `json:"entries"`
}

// KeyCode records one physical code assigned to a key spelling. Alias members
// and universally equivalent keys may share a code.
type KeyCode struct {
	Key  Key    `json:"key"`
	Code uint64 `json:"code"`
}

// Stats reports the lexicographic objective values.
type Stats struct {
	Pages          int `json:"pages"`
	ExceptionPages int `json:"exception_pages"`
	UsedCodes      int `json:"used_codes"`
}

// Plan is a deterministic, independently reviewable mapping.
type Plan struct {
	Entries    []MappedEntry   `json:"entries"`
	Partitions []PartitionPlan `json:"partitions"`
	KeyCodes   []KeyCode       `json:"key_codes"`
	Stats      Stats           `json:"stats"`
	Digest     string          `json:"digest"`
}

func buildPlan(n *normalized, codesByGroup map[int]uint64) (*Plan, error) {
	p := &Plan{}
	used := make(map[uint64]bool)
	for _, gid := range n.activeIDs {
		used[codesByGroup[gid]] = true
	}
	for _, part := range n.parts {
		pp := PartitionPlan{Name: part.name}
		pages, err := computePages(n, part, codesByGroup)
		if err != nil {
			return nil, err
		}
		pp.Pages = pages
		for _, e := range part.entries {
			newCode := codesByGroup[e.group]
			me := MappedEntry{Partition: part.name, OldCode: e.oldCode, Key: e.key, NewCode: newCode}
			pp.Entries = append(pp.Entries, me)
			p.Entries = append(p.Entries, me)
			used[newCode] = true
		}
		p.Partitions = append(p.Partitions, pp)
		if len(pages) > 0 {
			p.Stats.ExceptionPages += len(pages) - 1
		}
		p.Stats.Pages += len(pages)
	}
	// Key codes in deterministic key order.
	for _, gid := range n.activeIDs {
		g := n.groups[gid]
		for _, k := range g.members {
			p.KeyCodes = append(p.KeyCodes, KeyCode{Key: k, Code: codesByGroup[gid]})
		}
	}
	sort.Slice(p.KeyCodes, func(i, j int) bool { return p.KeyCodes[i].Key.Less(p.KeyCodes[j].Key) })
	p.Stats.UsedCodes = len(used)
	p.Digest = digestPlan(p)
	return p, nil
}

func digestPlan(p *Plan) string {
	h := sha256.New()
	for _, e := range p.Entries {
		fmt.Fprintf(h, "e|%s|%d|%d|%s|%d\n", e.Partition, e.OldCode, e.Key.Kind, e.Key.Text, e.NewCode)
	}
	for _, pp := range p.Partitions {
		for _, pg := range pp.Pages {
			fmt.Fprintf(h, "p|%s|%d|%d|%d|%d|%d\n", pp.Name, pg.OldLo, pg.OldHi, pg.Delta, pg.NewLo, pg.NewHi)
		}
	}
	for _, kc := range p.KeyCodes {
		fmt.Fprintf(h, "k|%d|%s|%d\n", kc.Key.Kind, kc.Key.Text, kc.Code)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// String renders a stable, human-reviewable summary.
func (p *Plan) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pages=%d exception_pages=%d used_codes=%d digest=%s\n", p.Stats.Pages, p.Stats.ExceptionPages, p.Stats.UsedCodes, p.Digest)
	for _, pp := range p.Partitions {
		fmt.Fprintf(&b, "partition %s:\n", pp.Name)
		for _, pg := range pp.Pages {
			fmt.Fprintf(&b, "  page [%d,%d] delta=%+d -> [%d,%d]\n", pg.OldLo, pg.OldHi, pg.Delta, pg.NewLo, pg.NewHi)
		}
		for _, e := range pp.Entries {
			fmt.Fprintf(&b, "  %d %s -> %d\n", e.OldCode, e.Key, e.NewCode)
		}
	}
	for _, kc := range p.KeyCodes {
		fmt.Fprintf(&b, "key %s = %d\n", kc.Key, kc.Code)
	}
	return b.String()
}

// Verify independently checks a plan against the original configuration.
// It is equivalent to the package-level Verify function.
func (p *Plan) Verify(cfg Config) error { return Verify(cfg, p) }

// Verify independently checks a plan against the original configuration.
// It re-derives every page and every query row set; no cached query result is
// trusted.
func Verify(cfg Config, plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("gdict: nil plan")
	}
	n, err := normalize(cfg)
	if err != nil {
		return err
	}
	codeByGroup, err := codesFromPlan(n, plan)
	if err != nil {
		return err
	}
	if err := verifyPlanCoversAllEntries(n, plan); err != nil {
		return err
	}
	d, err := buildDAG(n)
	if err != nil {
		return err
	}
	reachable := make([][]bool, d.n)
	for i := range reachable {
		reachable[i] = make([]bool, d.n)
	}
	for a := 0; a < d.n; a++ {
		var stack []int
		stack = append(stack, a)
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, y := range d.adj[x] {
				if !reachable[a][y] {
					reachable[a][y] = true
					stack = append(stack, y)
				}
			}
		}
	}
	// Basic code-space constraints.
	allowed := make(map[uint64]bool)
	for _, gid := range n.activeIDs {
		c := codeByGroup[gid]
		if c >= cfg.Space.Limit {
			return fmt.Errorf("gdict: verify: code %d outside global space", c)
		}
		if inIntervals(c, n.holes) {
			return fmt.Errorf("gdict: verify: code %d lies in a reserved hole", c)
		}
		if f := n.groups[gid].fixed; f != nil && *f != c {
			return fmt.Errorf("gdict: verify: fixed key %s is at %d, want %d", n.groups[gid].canonical, c, *f)
		}
		if !allowed[c] {
			allowed[c] = true
		} else {
			// Shared codes are only legal for groups equivalent in every
			// collation; check all pairs sharing this code below.
		}
	}
	// Shared-code pairs must be equivalent in every version and have no
	// ordering path between them.
	byCode := make(map[uint64][]int)
	for _, gid := range n.activeIDs {
		byCode[codeByGroup[gid]] = append(byCode[codeByGroup[gid]], gid)
	}
	for c, ids := range byCode {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := ids[i], ids[j]
				for vi := range n.versions {
					if n.groups[a].ranks[vi] != n.groups[b].ranks[vi] {
						return fmt.Errorf("gdict: verify: code %d shared by non-equivalent keys %s and %s", c, n.groups[a].canonical, n.groups[b].canonical)
					}
				}
				pa, pb := n.activePos[a], n.activePos[b]
				if reachable[pa][pb] || reachable[pb][pa] {
					return fmt.Errorf("gdict: verify: code %d shared by order-constrained keys %s and %s", c, n.groups[a].canonical, n.groups[b].canonical)
				}
			}
		}
	}
	// All DAG edges (collation and range constraints).
	for a := 0; a < d.n; a++ {
		for _, b := range d.adj[a] {
			if codeByGroup[n.activeIDs[a]] >= codeByGroup[n.activeIDs[b]] {
				return fmt.Errorf("gdict: verify: order edge %s -> %s violated (%d >= %d)", n.groups[n.activeIDs[a]].canonical, n.groups[n.activeIDs[b]].canonical, codeByGroup[n.activeIDs[a]], codeByGroup[n.activeIDs[b]])
			}
		}
	}
	// Page mapping and all range cuts.
	partByName := make(map[string]partitionInfo)
	for _, part := range n.parts {
		partByName[part.name] = part
	}
	seenPartPlan := make(map[string]bool)
	for _, pp := range plan.Partitions {
		part, ok := partByName[pp.Name]
		if !ok {
			return fmt.Errorf("gdict: verify: plan has unknown partition %q", pp.Name)
		}
		if seenPartPlan[pp.Name] {
			return fmt.Errorf("gdict: verify: duplicate partition plan %q", pp.Name)
		}
		seenPartPlan[pp.Name] = true
		if err := verifyPartitionEntries(part, pp, codeByGroup); err != nil {
			return err
		}
		if err := verifyPartitionPages(part, pp, codeByGroup); err != nil {
			return err
		}
	}
	for _, part := range n.parts {
		if !seenPartPlan[part.name] {
			return fmt.Errorf("gdict: verify: plan omits partition %q", part.name)
		}
	}
	// Lookups.
	for _, lk := range cfg.Lookups {
		part, ok := partByName[lk.Partition]
		if !ok {
			return fmt.Errorf("gdict: verify: lookup %q references unknown partition %q", lk.Name, lk.Partition)
		}
		version := lk.Version
		if version == "" {
			version = part.version
		}
		vi := -1
		for i, v := range n.versions {
			if v == version {
				vi = i
				break
			}
		}
		if vi < 0 {
			return fmt.Errorf("gdict: verify: lookup %q uses unknown version %q", lk.Name, version)
		}
		rank, ok := n.preorders[vi].Ranks[lk.Key]
		if !ok {
			return fmt.Errorf("gdict: verify: lookup %q key is not ranked", lk.Name)
		}
		oldRows := make(map[string]bool)
		for _, e := range part.entries {
			if n.groups[e.group].ranks[vi] == rank {
				oldRows[fmt.Sprintf("%d", e.oldCode)] = true
			}
		}
		newCodes := make(map[uint64]bool)
		for _, gid := range n.activeIDs {
			if n.groups[gid].ranks[vi] == rank {
				newCodes[codeByGroup[gid]] = true
			}
		}
		newRows := make(map[string]bool)
		for _, e := range part.entries {
			if newCodes[codeByGroup[e.group]] {
				newRows[fmt.Sprintf("%d", e.oldCode)] = true
			}
		}
		if !sameStringSet(oldRows, newRows) {
			return fmt.Errorf("gdict: verify: lookup %q changes row set: old=%v new=%v", lk.Name, sortedSet(oldRows), sortedSet(newRows))
		}
	}
	// Recompute stats and digest.
	rebuilt, err := buildPlanFromCodes(n, codeByGroup)
	if err != nil {
		return err
	}
	if rebuilt.Stats != plan.Stats {
		return fmt.Errorf("gdict: verify: stats mismatch got %+v want %+v", plan.Stats, rebuilt.Stats)
	}
	if plan.Digest != "" && rebuilt.Digest != plan.Digest {
		return fmt.Errorf("gdict: verify: mapping digest mismatch")
	}
	return nil
}

func codesFromPlan(n *normalized, plan *Plan) (map[int]uint64, error) {
	partByOld := make(map[string]map[uint64]entryInfo)
	for _, part := range n.parts {
		m := make(map[uint64]entryInfo)
		for _, e := range part.entries {
			m[e.oldCode] = e
		}
		partByOld[part.name] = m
	}
	out := make(map[int]uint64)
	// Fixed or otherwise active keys may have no row occurrence. Their code
	// is nevertheless part of the independently reviewable mapping.
	for _, kc := range plan.KeyCodes {
		gid := groupForKey(n, kc.Key)
		if gid < 0 {
			return nil, fmt.Errorf("gdict: verify: key code for unknown key %s", kc.Key)
		}
		if old, ok := out[gid]; ok && old != kc.Code {
			return nil, fmt.Errorf("gdict: verify: one logical key mapped to both %d and %d", old, kc.Code)
		}
		out[gid] = kc.Code
	}
	for _, e := range plan.Entries {
		m, ok := partByOld[e.Partition]
		if !ok {
			return nil, fmt.Errorf("gdict: verify: unknown partition %q in entry", e.Partition)
		}
		ent, ok := m[e.OldCode]
		if !ok {
			return nil, fmt.Errorf("gdict: verify: unknown old code %d in partition %q", e.OldCode, e.Partition)
		}
		if ent.key != e.Key {
			return nil, fmt.Errorf("gdict: verify: key mismatch at %s/%d", e.Partition, e.OldCode)
		}
		if old, ok := out[ent.group]; ok && old != e.NewCode {
			return nil, fmt.Errorf("gdict: verify: one logical key mapped to both %d and %d", old, e.NewCode)
		}
		out[ent.group] = e.NewCode
	}
	for _, gid := range n.activeIDs {
		if _, ok := out[gid]; !ok {
			return nil, fmt.Errorf("gdict: verify: plan omits active key %s", n.groups[gid].canonical)
		}
	}
	return out, nil
}

func buildPlanFromCodes(n *normalized, codeByGroup map[int]uint64) (*Plan, error) {
	return buildPlan(n, codeByGroup)
}

func verifyPartitionEntries(part partitionInfo, pp PartitionPlan, codeOf map[int]uint64) error {
	if len(pp.Entries) != len(part.entries) {
		return fmt.Errorf("gdict: verify: partition %s has %d mapped entries, want %d", part.name, len(pp.Entries), len(part.entries))
	}
	byOld := make(map[uint64]entryInfo, len(part.entries))
	for _, e := range part.entries {
		byOld[e.oldCode] = e
	}
	for _, me := range pp.Entries {
		e, ok := byOld[me.OldCode]
		if !ok {
			return fmt.Errorf("gdict: verify: partition %s has unknown mapped old code %d", part.name, me.OldCode)
		}
		if me.Key != e.key {
			return fmt.Errorf("gdict: verify: partition %s old code %d key mismatch", part.name, me.OldCode)
		}
		if me.NewCode != codeOf[e.group] {
			return fmt.Errorf("gdict: verify: partition %s old code %d code %d, plan says %d", part.name, me.OldCode, me.NewCode, codeOf[e.group])
		}
	}
	return nil
}

func verifyPartitionPages(part partitionInfo, pp PartitionPlan, codeOf map[int]uint64) error {
	// Pages are sorted and non-overlapping.
	for i, pg := range pp.Pages {
		if pg.OldLo > pg.OldHi {
			return fmt.Errorf("gdict: verify: inverted page [%d,%d] in %s", pg.OldLo, pg.OldHi, part.name)
		}
		if i > 0 && pp.Pages[i-1].OldHi >= pg.OldLo {
			return fmt.Errorf("gdict: verify: overlapping or unsorted pages in %s", part.name)
		}
	}
	pageOf := func(code uint64) (*Page, bool) {
		i := sort.Search(len(pp.Pages), func(i int) bool { return pp.Pages[i].OldHi >= code })
		if i < len(pp.Pages) && pp.Pages[i].OldLo <= code && code <= pp.Pages[i].OldHi {
			return &pp.Pages[i], true
		}
		return nil, false
	}
	for _, e := range part.entries {
		pg, ok := pageOf(e.oldCode)
		if !ok {
			return fmt.Errorf("gdict: verify: no page covers entry %s/%d", part.name, e.oldCode)
		}
		got, ok := addDelta(e.oldCode, pg.Delta)
		if !ok || got != codeOf[e.group] {
			return fmt.Errorf("gdict: verify: entry %s/%d page maps to %d, code says %d", part.name, e.oldCode, got, codeOf[e.group])
		}
	}
	for _, c := range part.cuts {
		pg, ok := pageOf(c.OldCode)
		if !ok {
			return fmt.Errorf("gdict: verify: no page covers cut %s/%s at %d", part.name, c.Name, c.OldCode)
		}
		bound, ok := addDelta(c.OldCode, pg.Delta)
		if !ok {
			return fmt.Errorf("gdict: verify: cut %s/%s translated bound overflows", part.name, c.Name)
		}
		oldSet := make(map[uint64]bool)
		newSet := make(map[uint64]bool)
		for _, e := range part.entries {
			oldIn := e.oldCode < c.OldCode
			if c.Bound == LE {
				oldIn = e.oldCode <= c.OldCode
			}
			newIn := codeOf[e.group] < bound
			if c.Bound == LE {
				newIn = codeOf[e.group] <= bound
			}
			if oldIn {
				oldSet[e.oldCode] = true
			}
			if newIn {
				newSet[e.oldCode] = true
			}
		}
		if !sameUintSet(oldSet, newSet) {
			return fmt.Errorf("gdict: verify: cut %s/%s changes row set: old=%v new=%v", part.name, c.Name, sortedUintSet(oldSet), sortedUintSet(newSet))
		}
	}
	return nil
}

func groupForKey(n *normalized, k Key) int {
	for i := range n.groups {
		for _, m := range n.groups[i].members {
			if m == k {
				return i
			}
		}
	}
	return -1
}

func sameUintSet(a, b map[uint64]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
func sortedUintSet(m map[uint64]bool) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func sameStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func verifyPlanCoversAllEntries(n *normalized, plan *Plan) error {
	type id struct {
		part string
		old  uint64
	}
	seen := make(map[id]bool)
	for _, e := range plan.Entries {
		k := id{e.Partition, e.OldCode}
		if seen[k] {
			return fmt.Errorf("gdict: verify: duplicate mapped entry %s/%d", e.Partition, e.OldCode)
		}
		seen[k] = true
	}
	for _, part := range n.parts {
		for _, e := range part.entries {
			if !seen[id{part.name, e.oldCode}] {
				return fmt.Errorf("gdict: verify: plan omits entry %s/%d", part.name, e.oldCode)
			}
		}
	}
	if len(seen) != len(plan.Entries) {
		return fmt.Errorf("gdict: verify: duplicate mapped entries")
	}
	return nil
}

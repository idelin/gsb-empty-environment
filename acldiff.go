// Package offlinecli implements an offline IPv4 ACL differ.
//
// Each ACL is an ordered list of [CIDR, allow|deny] rules, evaluated
// top-down with first match winning and an implicit default deny.
// Diff returns the minimal set of aligned CIDR blocks whose effective
// action changed between the two ACLs.
package offlinecli

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"net/netip"
	"sort"
)

const (
	ActionAllow = "allow"
	ActionDeny  = "deny"
)

// Change is one differing address range, inclusive on both ends.
type Change struct {
	Lo, Hi   uint32
	OldAllow bool
	NewAllow bool
}

// rule is a normalized ACL rule: an inclusive IPv4 interval plus action.
type rule struct {
	lo, hi uint32
	allow  bool
}

// ParseRule converts a [CIDR, action] pair into a normalized rule.
// Host bits may be set; the network is normalized by its mask.
func ParseRule(pair []string) (rule, error) {
	var r rule
	if len(pair) != 2 {
		return r, fmt.Errorf("rule must be [CIDR, action], got %d fields", len(pair))
	}
	p, err := netip.ParsePrefix(pair[0])
	if err != nil {
		addr, addrErr := netip.ParseAddr(pair[0])
		if addrErr != nil {
			return r, fmt.Errorf("invalid CIDR %q: %w", pair[0], err)
		}
		if addr.Is4() {
			p = netip.PrefixFrom(addr, 32)
		} else {
			return r, fmt.Errorf("not an IPv4 CIDR: %q", pair[0])
		}
	}
	if !p.Addr().Is4() {
		return r, fmt.Errorf("not an IPv4 CIDR: %q", pair[0])
	}
	switch pair[1] {
	case ActionAllow:
		r.allow = true
	case ActionDeny:
		r.allow = false
	default:
		return r, fmt.Errorf("invalid action %q (want allow|deny)", pair[1])
	}
	p = p.Masked()
	a4 := p.Addr().As4()
	r.lo = binary.BigEndian.Uint32(a4[:])
	r.hi = uint32(uint64(r.lo) + (uint64(1)<<(32-p.Bits()) - 1))
	return r, nil
}

// ParseRules parses a whole ACL, preserving rule order.
func ParseRules(pairs [][]string) ([]rule, error) {
	rules := make([]rule, 0, len(pairs))
	for i, pair := range pairs {
		r, err := ParseRule(pair)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// interval is an inclusive IPv4 range.
type interval struct {
	lo, hi uint32
}

// allowIntervals computes the effective allow set of an ACL as sorted,
// disjoint, non-adjacent intervals. It sweeps interval boundaries while
// tracking the highest-priority (lowest-index) active rule, so duplicate
// and shadowed rules are handled naturally in O(n log n).
func allowIntervals(rules []rule) []interval {
	type event struct {
		pos uint64 // hi+1 may be 2^32, so events use uint64
		idx int
		add bool
	}
	events := make([]event, 0, 2*len(rules))
	for i, r := range rules {
		events = append(events, event{pos: uint64(r.lo), idx: i, add: true})
		events = append(events, event{pos: uint64(r.hi) + 1, idx: i, add: false})
	}
	sort.Slice(events, func(a, b int) bool {
		if events[a].pos != events[b].pos {
			return events[a].pos < events[b].pos
		}
		return events[a].idx < events[b].idx
	})

	// Lazy-deletion min-heaps over rule indices.
	added := &indexHeap{}
	removed := &indexHeap{}
	top := func() (int, bool) {
		for added.Len() > 0 && removed.Len() > 0 && (*added)[0] == (*removed)[0] {
			popIndex(added)
			popIndex(removed)
		}
		if added.Len() == 0 {
			return 0, false
		}
		return (*added)[0], true
	}

	var out []interval
	for i := 0; i < len(events); {
		pos := events[i].pos
		for ; i < len(events) && events[i].pos == pos; i++ {
			if events[i].add {
				pushIndex(added, events[i].idx)
			} else {
				pushIndex(removed, events[i].idx)
			}
		}
		if i >= len(events) {
			break
		}
		next := events[i].pos
		if idx, ok := top(); ok && rules[idx].allow {
			lo, hi := uint32(pos), uint32(next-1)
			if n := len(out); n > 0 && uint64(out[n-1].hi)+1 == uint64(lo) {
				out[n-1].hi = hi
			} else {
				out = append(out, interval{lo, hi})
			}
		}
	}
	return out
}

// subtract returns a \ b for sorted disjoint interval lists.
func subtract(a, b []interval) []interval {
	var out []interval
	j := 0
	for _, iv := range a {
		lo, hi := uint64(iv.lo), uint64(iv.hi)
		for j < len(b) && uint64(b[j].hi) < lo {
			j++
		}
		cur := lo
		for k := j; k < len(b) && uint64(b[k].lo) <= hi; k++ {
			if uint64(b[k].lo) > cur {
				out = append(out, interval{uint32(cur), uint32(uint64(b[k].lo) - 1)})
			}
			cur = uint64(b[k].hi) + 1
			if cur > hi {
				break
			}
		}
		if cur <= hi {
			out = append(out, interval{uint32(cur), uint32(hi)})
		}
	}
	return out
}

// DiffACLs computes the address ranges whose effective action differs
// between the old and new ACLs, sorted by start address. Adjacent ranges
// with the same change direction are merged.
func DiffACLs(oldRules, newRules []rule) []Change {
	oldAllow := allowIntervals(oldRules)
	newAllow := allowIntervals(newRules)
	lost := subtract(oldAllow, newAllow) // old=allow, new=deny
	gained := subtract(newAllow, oldAllow)
	changes := make([]Change, 0, len(lost)+len(gained))
	i, j := 0, 0
	for i < len(lost) || j < len(gained) {
		switch {
		case j >= len(gained) || (i < len(lost) && lost[i].lo < gained[j].lo):
			changes = append(changes, Change{Lo: lost[i].lo, Hi: lost[i].hi, OldAllow: true, NewAllow: false})
			i++
		default:
			changes = append(changes, Change{Lo: gained[j].lo, Hi: gained[j].hi, OldAllow: false, NewAllow: true})
			j++
		}
	}
	return changes
}

// ChangeCIDRs decomposes one change range into the minimal set of
// aligned CIDR blocks, rendered as "a.b.c.d/prefix".
func ChangeCIDRs(c Change) []string {
	var out []string
	start, end := uint64(c.Lo), uint64(c.Hi)
	for start <= end {
		align := uint64(1) << 32 // a block at 0.0.0.0 may span the whole space
		if start != 0 {
			align = start & (^start + 1) // lowest set bit = largest aligned block
		}
		size := align
		if remain := end - start + 1; size > remain {
			size = uint64(1) << (63 - bits.LeadingZeros64(remain))
		}
		prefix := 32 - bits.TrailingZeros64(size)
		ip := netip.AddrFrom4([4]byte{byte(start >> 24), byte(start >> 16), byte(start >> 8), byte(start)})
		out = append(out, fmt.Sprintf("%s/%d", ip, prefix))
		start += size
	}
	return out
}

// DiffEntries renders the full diff as [CIDR, oldAction, newAction] rows,
// sorted by start address ascending.
func DiffEntries(oldRules, newRules []rule) [][]string {
	entries := [][]string{}
	for _, c := range DiffACLs(oldRules, newRules) {
		oldAction, newAction := ActionDeny, ActionAllow
		if c.OldAllow {
			oldAction, newAction = ActionAllow, ActionDeny
		}
		for _, cidr := range ChangeCIDRs(c) {
			entries = append(entries, []string{cidr, oldAction, newAction})
		}
	}
	return entries
}

// indexHeap is a concrete min-heap of rule indices with lazy deletion
// support via a paired removal heap in the caller.
type indexHeap []int

func (h *indexHeap) Len() int { return len(*h) }

func pushIndex(h *indexHeap, v int) {
	*h = append(*h, v)
	for i := len(*h) - 1; i > 0; {
		p := (i - 1) / 2
		if (*h)[p] <= (*h)[i] {
			break
		}
		(*h)[p], (*h)[i] = (*h)[i], (*h)[p]
		i = p
	}
}

func popIndex(h *indexHeap) int {
	old := *h
	n := len(old)
	top := old[0]
	old[0] = old[n-1]
	*h = old[:n-1]
	for i := 0; ; {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(*h) && (*h)[l] < (*h)[m] {
			m = l
		}
		if r < len(*h) && (*h)[r] < (*h)[m] {
			m = r
		}
		if m == i {
			break
		}
		(*h)[m], (*h)[i] = (*h)[i], (*h)[m]
		i = m
	}
	return top
}

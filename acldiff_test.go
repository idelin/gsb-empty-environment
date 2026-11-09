package offlinecli

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// evalDirect evaluates an ACL the slow, obviously-correct way:
// top-down, first match wins, default deny.
func evalDirect(rules []rule, ip uint32) bool {
	for _, r := range rules {
		if r.lo <= ip && ip <= r.hi {
			return r.allow
		}
	}
	return false
}

func mustParse(t *testing.T, pairs [][]string) []rule {
	t.Helper()
	rules, err := ParseRules(pairs)
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	return rules
}

func TestOverlappingShadowedAndDuplicateRules(t *testing.T) {
	oldACL := [][]string{
		{"10.0.0.0/8", "deny"},
		{"10.1.0.0/16", "allow"}, // fully shadowed by the deny above
		{"10.2.0.0/16", "allow"}, // fully shadowed too
		{"192.168.0.0/16", "allow"},
		{"192.168.0.0/16", "allow"}, // exact duplicate
		{"192.168.1.0/24", "deny"},  // shadowed by the /16 allow above
		{"172.16.0.0/12", "deny"},
	}
	newACL := [][]string{
		{"10.1.2.3/16", "allow"}, // host bits set: normalizes to 10.1.0.0/16
		{"10.0.0.0/8", "deny"},
		{"192.168.1.0/24", "deny"},
		{"192.168.0.0/16", "allow"}, // /24 hole carved by the deny above
		{"172.16.0.0/12", "deny"},
	}
	got := DiffEntries(mustParse(t, oldACL), mustParse(t, newACL))
	want := [][]string{
		{"10.1.0.0/16", "deny", "allow"},
		{"192.168.1.0/24", "allow", "deny"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSlashZeroNeverEnumerates(t *testing.T) {
	oldACL := [][]string{{"0.0.0.0/0", "allow"}}
	newACL := [][]string{
		{"203.0.113.0/24", "deny"},
		{"0.0.0.0/0", "allow"},
	}
	got := DiffEntries(mustParse(t, oldACL), mustParse(t, newACL))
	want := [][]string{
		{"203.0.113.0/24", "allow", "deny"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// Full-space flip must collapse to a single /0 row.
	got = DiffEntries(mustParse(t, [][]string{{"0.0.0.0/0", "deny"}}), mustParse(t, oldACL))
	want = [][]string{{"0.0.0.0/0", "deny", "allow"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAdjacentOppositeDirectionsStaySplit(t *testing.T) {
	oldACL := [][]string{{"10.0.0.0/25", "allow"}}
	newACL := [][]string{{"10.0.0.128/25", "allow"}}
	got := DiffEntries(mustParse(t, oldACL), mustParse(t, newACL))
	want := [][]string{
		{"10.0.0.0/25", "allow", "deny"},
		{"10.0.0.128/25", "deny", "allow"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// cidrInterval parses an emitted CIDR row back into an inclusive range.
func cidrInterval(t *testing.T, cidr string) (uint32, uint32) {
	t.Helper()
	r, err := ParseRule([]string{cidr, "allow"})
	if err != nil {
		t.Fatalf("emitted CIDR %q does not parse: %v", cidr, err)
	}
	return r.lo, r.hi
}

// replayDiff verifies that the diff rows exactly reproduce the behavior
// change: addresses inside a row evaluate to its stated old/new actions
// under direct evaluation, and every sampled address outside all rows
// evaluates identically under both ACLs. maxEntries caps how many rows
// get their interiors spot-checked (evenly strided); <= 0 checks all.
func replayDiff(t *testing.T, oldRules, newRules []rule, entries [][]string, samples []uint32, maxEntries int) {
	t.Helper()
	type span struct {
		lo, hi   uint32
		oldAllow bool
	}
	spans := make([]span, 0, len(entries))
	var prevHi uint64
	stride := 1
	if maxEntries > 0 && len(entries) > maxEntries {
		stride = (len(entries) + maxEntries - 1) / maxEntries
	}
	for i, e := range entries {
		if len(e) != 3 {
			t.Fatalf("entry %d: want 3 fields, got %v", i, e)
		}
		lo, hi := cidrInterval(t, e[0])
		if uint64(lo) < prevHi {
			t.Fatalf("entries not sorted/disjoint at %d: %v", i, e)
		}
		prevHi = uint64(hi) + 1
		var oldAllow bool
		switch {
		case e[1] == ActionAllow && e[2] == ActionDeny:
			oldAllow = true
		case e[1] == ActionDeny && e[2] == ActionAllow:
			oldAllow = false
		default:
			t.Fatalf("entry %d: bad action pair %v", i, e)
		}
		spans = append(spans, span{lo, hi, oldAllow})

		if i%stride != 0 {
			continue
		}
		// Replay inside the block: boundaries plus up to 8 interior points.
		size := uint64(hi) - uint64(lo) + 1
		points := []uint64{uint64(lo), uint64(hi)}
		for k := uint64(1); k < 8 && k < size; k++ {
			points = append(points, uint64(lo)+k*size/8)
		}
		for _, p := range points {
			ip := uint32(p)
			if got := evalDirect(oldRules, ip); got != oldAllow {
				t.Fatalf("replay old ACL at %d (%s): got allow=%v, entry says %s",
					ip, e[0], got, e[1])
			}
			if got := evalDirect(newRules, ip); got == oldAllow {
				t.Fatalf("replay new ACL at %d (%s): got allow=%v, entry says %s",
					ip, e[0], got, e[2])
			}
		}
	}

	covered := func(ip uint32) (span, bool) {
		i := sort.Search(len(spans), func(i int) bool { return spans[i].hi >= ip })
		if i < len(spans) && spans[i].lo <= ip {
			return spans[i], true
		}
		return span{}, false
	}
	for _, ip := range samples {
		o, n := evalDirect(oldRules, ip), evalDirect(newRules, ip)
		s, ok := covered(ip)
		switch {
		case o != n && !ok:
			t.Fatalf("address %d changed (%v->%v) but no diff row covers it", ip, o, n)
		case o != n && s.oldAllow != o:
			t.Fatalf("address %d: diff row direction %v contradicts eval %v->%v", ip, s.oldAllow, o, n)
		case o == n && ok:
			t.Fatalf("address %d unchanged (%v) but covered by diff row", ip, o)
		}
	}
}

func boundarySamples(rulesets ...[]rule) []uint32 {
	var out []uint32
	for _, rules := range rulesets {
		for _, r := range rules {
			if r.lo > 0 {
				out = append(out, r.lo-1)
			}
			out = append(out, r.lo, r.hi)
			if r.hi < 0xFFFFFFFF {
				out = append(out, r.hi+1)
			}
		}
	}
	return out
}

func TestReplayRandomizedOverlappingACLs(t *testing.T) {
	rng := rand.New(rand.NewSource(20261109))
	for trial := 0; trial < 200; trial++ {
		gen := func() [][]string {
			n := 1 + rng.Intn(40)
			pairs := make([][]string, 0, n)
			for i := 0; i < n; i++ {
				// Bias toward small blocks, but include wide masks and /0.
				bits := 32
				switch r := rng.Intn(100); {
				case r < 2:
					bits = 0
				case r < 15:
					bits = 1 + rng.Intn(8)
				case r < 45:
					bits = 8 + rng.Intn(8)
				default:
					bits = 16 + rng.Intn(17)
				}
				// Keep host bits set with 50% probability to exercise
				// mask normalization.
				ip := rng.Uint32()
				action := ActionAllow
				if rng.Intn(2) == 0 {
					action = ActionDeny
				}
				pairs = append(pairs, []string{fmt.Sprintf("%d.%d.%d.%d/%d",
					byte(ip>>24), byte(ip>>16), byte(ip>>8), byte(ip), bits), action})
				// 20% chance: duplicate the previous rule verbatim.
				if len(pairs) > 1 && rng.Intn(5) == 0 {
					pairs = append(pairs, pairs[len(pairs)-2])
				}
			}
			return pairs
		}
		oldPairs, newPairs := gen(), gen()
		oldRules := mustParse(t, oldPairs)
		newRules := mustParse(t, newPairs)
		entries := DiffEntries(oldRules, newRules)
		samples := boundarySamples(oldRules, newRules)
		for k := 0; k < 64; k++ {
			samples = append(samples, rng.Uint32())
		}
		replayDiff(t, oldRules, newRules, entries, samples, 0)
	}
}

func TestScaleHundredThousandRules(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scale test in -short mode")
	}
	rng := rand.New(rand.NewSource(1))
	gen := func() []rule {
		pairs := make([][]string, 0, 100000)
		for i := 1; i < 100000; i++ {
			ip := rng.Uint32()
			bits := 8 + rng.Intn(25)
			action := ActionAllow
			if rng.Intn(2) == 0 {
				action = ActionDeny
			}
			pairs = append(pairs, []string{fmt.Sprintf("%d.%d.%d.%d/%d",
				byte(ip>>24), byte(ip>>16), byte(ip>>8), byte(ip), bits), action})
		}
		pairs = append(pairs, []string{"0.0.0.0/0", "deny"}) // explicit default, forces full-space sweep
		return mustParse(t, pairs)
	}
	oldRules, newRules := gen(), gen()
	entries := DiffEntries(oldRules, newRules)
	samples := make([]uint32, 0, 512)
	for k := 0; k < 512; k++ {
		samples = append(samples, rng.Uint32())
	}
	replayDiff(t, oldRules, newRules, entries, samples, 256)
	t.Logf("100k+100k rules -> %d diff rows", len(entries))
}

func TestInvalidInput(t *testing.T) {
	for _, pairs := range [][][]string{
		{{"10.0.0.0/8"}},
		{{"10.0.0.0/8", "permit"}},
		{{"10.0.0.0/33", "allow"}},
		{{"2001:db8::/32", "allow"}},
		{{"not-an-ip", "allow"}},
	} {
		if _, err := ParseRules(pairs); err == nil {
			t.Fatalf("ParseRules(%v) succeeded, want error", pairs)
		}
	}
}

package devpolicy

import (
	"math/rand"
	"testing"
)

// bruteMask is the reference semantics evaluated by exhaustive point
// enumeration over a small domain. It independently replays every layer's
// accumulator and ANDs the layer results, exactly mirroring the spec text.
func bruteMask(rules []Rule, t DevType, major, minor uint16) uint8 {
	depth := 0
	for _, r := range rules {
		if r.Layer+1 > depth {
			depth = r.Layer + 1
		}
	}
	effective := uint8(MaskAll)
	for layer := 0; layer < depth; layer++ {
		mask := uint8(0)
		for _, r := range rules {
			if r.Layer != layer || (r.Type != TypeAll && r.Type != t) {
				continue
			}
			if major < r.MajorMin || major > r.MajorMax ||
				minor < r.MinorMin || minor > r.MinorMax {
				continue
			}
			mask = foldMask(mask, r.Effect, r.Perm)
		}
		effective &= mask
	}
	return effective
}

// assertCover verifies a partition is an exact disjoint cover of the domain.
func assertCover(t *testing.T, p Partition, domainMax uint16) {
	t.Helper()
	area := 0
	for i, cell := range p {
		if cell.Rect.empty() {
			t.Fatalf("cell %d is empty", i)
		}
		if cell.MajMax > domainMax || cell.MinMax > domainMax {
			t.Fatalf("cell %d escapes domain: %+v", i, cell.Rect)
		}
		area += int(cell.MajMax-cell.MajMin+1) * int(cell.MinMax-cell.MinMin+1)
		for j := i + 1; j < len(p); j++ {
			// Edge-adjacent cells share no interior point; intersect reports a real
			// positive overlap only.
			if _, ok := cell.Rect.intersect(p[j].Rect); ok {
				t.Fatalf("cells %d and %d overlap: %+v vs %+v", i, j, cell.Rect, p[j].Rect)
			}
		}
	}
	want := int(domainMax+1) * int(domainMax+1)
	if area != want {
		t.Fatalf("partition covers %d points, want %d", area, want)
	}
}

// exhaustivelyCompare compiles on a small domain and checks every device
// against the brute-force reference for both types and all three bits.
func exhaustivelyCompare(t *testing.T, rules []Rule, domainMax uint16) *CompiledPolicy {
	t.Helper()
	compiled, err := (&Policy{Rules: rules}).CompileDomain(domainMax)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for idx, typ := range orderedTypes() {
		assertCover(t, compiled.Effective[idx].Partition, domainMax)
		for major := uint16(0); major <= domainMax; major++ {
			for minor := uint16(0); minor <= domainMax; minor++ {
				got := compiled.EffectiveMask(typ, major, minor)
				want := bruteMask(rules, typ, major, minor)
				if got != want {
					t.Fatalf("type %c (%d,%d): diagram=0b%03b brute=0b%03b",
						typ, major, minor, got, want)
				}
			}
		}
	}
	return compiled
}

// randomPolicy builds a layered policy with overlapping wildcard intervals.
func randomPolicy(rng *rand.Rand, maxLayer, maxRule int, domainMax uint16) []Rule {
	n := 1 + rng.Intn(maxRule)
	types := []DevType{TypeChar, TypeBlock, TypeAll}
	effects := []RuleEffect{EffectAllow, EffectDeny}
	perms := []uint8{BitR, BitW, BitM, BitR | BitW, MaskAll, BitR | BitM}
	rules := make([]Rule, 0, n)
	for i := 0; i < n; i++ {
		majLo := uint16(rng.Intn(int(domainMax) + 1))
		majHi := majLo + uint16(rng.Intn(int(domainMax)-int(majLo)+1))
		minLo := uint16(rng.Intn(int(domainMax) + 1))
		minHi := minLo + uint16(rng.Intn(int(domainMax)-int(minLo)+1))
		if rng.Intn(3) == 0 {
			majLo, majHi = 0, domainMax
		}
		if rng.Intn(3) == 0 {
			minLo, minHi = 0, domainMax
		}
		rules = append(rules, Rule{
			Layer:    rng.Intn(maxLayer + 1),
			Order:    i,
			Effect:   effects[rng.Intn(2)],
			Type:     types[rng.Intn(3)],
			MajorMin: majLo, MajorMax: majHi,
			MinorMin: minLo, MinorMax: minHi,
			Perm: perms[rng.Intn(len(perms))],
			Name: "r" + itoa(i),
		})
	}
	return rules
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

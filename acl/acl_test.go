package acl

import (
	"math/rand"
	"net/netip"
	"testing"
)

func mustRule(t *testing.T, cidr, action string) Rule {
	t.Helper()
	rule, err := ParseRule(cidr, action)
	if err != nil {
		t.Fatalf("ParseRule(%q,%q): %v", cidr, action, err)
	}
	return rule
}

func rules(pairs ...[2]string) []Rule {
	out := make([]Rule, 0, len(pairs))
	for _, pair := range pairs {
		rule, err := ParseRule(pair[0], pair[1])
		if err != nil {
			panic(err)
		}
		out = append(out, rule)
	}
	return out
}

func blockStrings(blocks []Block) [][3]string {
	out := make([][3]string, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, [3]string{
			block.Prefix().String(),
			ActionName(block.Old),
			ActionName(block.New),
		})
	}
	return out
}

func TestParseRuleMasksHostBits(t *testing.T) {
	rule, err := ParseRule("10.1.2.3/8", "allow")
	if err != nil {
		t.Fatal(err)
	}
	if got := rule.Prefix.String(); got != "10.0.0.0/8" {
		t.Fatalf("masked prefix = %q, want 10.0.0.0/8", got)
	}
}

func TestNonZeroHostBitsMatchAsMaskedPrefix(t *testing.T) {
	old := rules([2]string{"10.1.2.3/24", "allow"})
	new := rules()
	blocks, err := Diff(old, new)
	if err != nil {
		t.Fatal(err)
	}
	got := blockStrings(blocks)
	want := [][3]string{{"10.1.2.0/24", "allow", "deny"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("blocks = %v, want %v", got, want)
	}

	compiled := Compile(old)
	for _, sample := range []uint32{0x0A010200, 0x0A0102FF, 0x0A010203} {
		if got := resolve(old, compiled.LookupRule(sample)); got != Allow {
			t.Fatalf("address %08x should match the masked prefix", sample)
		}
	}
	if got := resolve(old, compiled.LookupRule(0x0A010300)); got != Deny {
		t.Fatal("10.1.3.0 must not match 10.1.2.0/24")
	}
}

func TestDefaultDenyVersusAllowAll(t *testing.T) {
	old := rules()
	new := rules([2]string{"0.0.0.0/0", "allow"})

	blocks, err := Diff(old, new)
	if err != nil {
		t.Fatal(err)
	}
	got := blockStrings(blocks)
	want := [][3]string{{"0.0.0.0/0", "deny", "allow"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("blocks = %v, want %v", got, want)
	}
}

func TestIdenticalACLsProduceNoDifference(t *testing.T) {
	aclRules := rules(
		[2]string{"10.0.0.0/8", "allow"},
		[2]string{"10.1.0.0/16", "deny"},
		[2]string{"192.168.0.0/16", "allow"},
	)
	blocks, err := Diff(aclRules, aclRules)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Fatalf("expected no blocks, got %v", blockStrings(blocks))
	}
}

func TestFirstMatchShadowing(t *testing.T) {
	// The broad allow is shadowed inside 10.1.0.0/16 by the earlier deny.
	old := rules(
		[2]string{"10.1.0.0/16", "deny"},
		[2]string{"10.0.0.0/8", "allow"},
	)
	new := rules(
		[2]string{"10.0.0.0/8", "allow"},
	)
	blocks, err := Diff(old, new)
	if err != nil {
		t.Fatal(err)
	}
	got := blockStrings(blocks)
	want := [][3]string{{"10.1.0.0/16", "deny", "allow"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("blocks = %v, want %v", got, want)
	}
}

func TestDuplicatePrefixesKeepFirstRule(t *testing.T) {
	old := rules(
		[2]string{"10.0.0.0/8", "allow"},
		[2]string{"10.0.0.0/8", "deny"},
	)
	new := rules([2]string{"10.0.0.0/8", "allow"})
	blocks, err := Diff(old, new)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Fatalf("duplicate later rule must be ignored, got %v", blockStrings(blocks))
	}
}

func TestAdjacentBlocksMergeToLargestAlignedCIDR(t *testing.T) {
	// Allowing two neighboring /25s must collapse into one /24.
	old := rules()
	new := rules(
		[2]string{"192.0.2.0/25", "allow"},
		[2]string{"192.0.2.128/25", "allow"},
	)
	blocks, err := Diff(old, new)
	if err != nil {
		t.Fatal(err)
	}
	got := blockStrings(blocks)
	want := [][3]string{{"192.0.2.0/24", "deny", "allow"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("blocks = %v, want %v", got, want)
	}
}

func TestOppositeDirectionsDoNotMerge(t *testing.T) {
	// Sibling /25s changing in opposite directions stay separate.
	old := rules(
		[2]string{"192.0.2.0/25", "allow"},
		[2]string{"192.0.2.128/25", "deny"},
	)
	new := rules(
		[2]string{"192.0.2.0/25", "deny"},
		[2]string{"192.0.2.128/25", "allow"},
	)
	blocks, err := Diff(old, new)
	if err != nil {
		t.Fatal(err)
	}
	got := blockStrings(blocks)
	if len(got) != 2 {
		t.Fatalf("blocks = %v, want two /25 entries", got)
	}
	if got[0][0] != "192.0.2.0/25" || got[1][0] != "192.0.2.128/25" {
		t.Fatalf("blocks not sorted by start: %v", got)
	}
}

func TestBlockPrefixRoundTrip(t *testing.T) {
	cases := []struct {
		start uint32
		size  uint64
		want  string
	}{
		{0, 1 << 32, "0.0.0.0/0"},
		{0x0A000000, 1 << 24, "10.0.0.0/8"},
		{0xC0000200, 1 << 8, "192.0.2.0/24"},
		{0xC0000280, 1, "192.0.2.128/32"},
	}
	for _, tc := range cases {
		block := Block{Start: tc.start, Size: tc.size, Old: Deny, New: Allow}
		if got := block.Prefix().String(); got != tc.want {
			t.Errorf("Prefix() = %q, want %q", got, tc.want)
		}
	}
}

func TestPrefixContains(t *testing.T) {
	prefix := netip.MustParsePrefix("10.0.0.0/8")
	if !prefixContains(prefix, 0x0AFFFFFF) {
		t.Error("10.255.255.255 should be inside 10.0.0.0/8")
	}
	if prefixContains(prefix, 0x0B000000) {
		t.Error("11.0.0.0 should be outside 10.0.0.0/8")
	}
	zero := netip.MustParsePrefix("0.0.0.0/0")
	if !prefixContains(zero, 0xFFFFFFFF) {
		t.Error("/0 must contain every address without enumeration")
	}
}

func TestVerifyAcrossWholeSpaceBlock(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	rulesOld := rules([2]string{"0.0.0.0/0", "deny"})
	rulesNew := rules([2]string{"0.0.0.0/0", "allow"})
	blocks, err := Diff(rulesOld, rulesNew)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Prefix().String() != "0.0.0.0/0" {
		t.Fatalf("want single /0 block, got %v", blockStrings(blocks))
	}
	if err := Verify(rulesOld, rulesNew, blocks, 500, rng); err != nil {
		t.Fatalf("verify over the full address space: %v", err)
	}
}

func TestParseRuleRejectsIPv6(t *testing.T) {
	if _, err := ParseRule("2001:db8::/32", "allow"); err == nil {
		t.Fatal("IPv6 prefix must be rejected")
	}
	if _, err := ParseRule("not-a-cidr", "allow"); err == nil {
		t.Fatal("malformed CIDR must be rejected")
	}
	if _, err := ParseRule("10.0.0.0/8", "permit"); err == nil {
		t.Fatal("unknown action must be rejected")
	}
}

func TestMergedBlocksStayDisjoint(t *testing.T) {
	rng := rand.New(rand.NewSource(123))
	for iteration := 0; iteration < 50; iteration++ {
		blocks, err := Diff(randomRules(rng, 80), randomRules(rng, 80))
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(blocks); i++ {
			prevEnd := uint64(blocks[i-1].Start) + blocks[i-1].Size
			if prevEnd > uint64(blocks[i].Start) {
				t.Fatalf("blocks overlap: %s and %s",
					blocks[i-1].Prefix(), blocks[i].Prefix())
			}
		}
	}
}

func TestRandomizedDiffMatchesReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 200; iteration++ {
		rulesOld := randomRules(rng, 50)
		rulesNew := randomRules(rng, 50)

		blocks, err := Diff(rulesOld, rulesNew)
		if err != nil {
			t.Fatal(err)
		}
		if err := Verify(rulesOld, rulesNew, blocks, 300, rng); err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
	}
}

func randomRules(rng *rand.Rand, count int) []Rule {
	out := make([]Rule, 0, count)
	for i := 0; i < count; i++ {
		bits := rng.Intn(33)
		base := rng.Uint32()
		addr := netip.AddrFrom4([4]byte{
			byte(base >> 24), byte(base >> 16), byte(base >> 8), byte(base),
		})
		prefix := netip.PrefixFrom(addr, bits).Masked()
		action := Deny
		if rng.Intn(2) == 0 {
			action = Allow
		}
		out = append(out, Rule{Prefix: prefix, Action: action})
	}
	return out
}

func TestScaleHundredThousandRules(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test skipped in -short mode")
	}
	rng := rand.New(rand.NewSource(7))
	rulesOld := randomRules(rng, 100000)
	rulesNew := randomRules(rng, 100000)

	blocks, err := Diff(rulesOld, rulesNew)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(rulesOld, rulesNew, blocks, 500, rng); err != nil {
		t.Fatal(err)
	}

	// Every emitted prefix must be aligned and cover a power-of-two range.
	for _, block := range blocks {
		if block.Size&(block.Size-1) != 0 {
			t.Fatalf("block size %d is not a power of two", block.Size)
		}
		if uint64(block.Start)%block.Size != 0 {
			t.Fatalf("block %s is not aligned", block.Prefix())
		}
	}
	t.Logf("scale run produced %d difference blocks", len(blocks))
}

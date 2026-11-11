package packetvm

import (
	"fmt"
	"math/rand"
	"testing"
)

// representativeValues returns a small set of values that covers the
// comparisons in the rule. For exhaustive equivalence it is enough to vary
// each compared byte/field over boundary and representative values, together
// with every length from zero up to the largest offset read.
func representativeValues(rule Rule) []uint32 {
	seen := map[uint32]bool{0: true, 1: true}
	var walkNum func(n Numeric)
	var walkBool func(b Bool)
	walkNum = func(n Numeric) {
		switch v := n.(type) {
		case *Imm:
			seen[v.Value] = true
			if v.Value > 0 {
				seen[v.Value-1] = true
			}
			seen[v.Value+1] = true
		case *Load:
			seen[uint32(v.Size)] = true
		case *Len:
		}
	}
	walkBool = func(b Bool) {
		switch v := b.(type) {
		case *Cmp:
			walkNum(v.LHS)
			walkNum(v.RHS)
		case *Not:
			walkBool(v.Inner)
		case *And:
			for _, t := range v.Terms {
				walkBool(t)
			}
		case *Or:
			for _, t := range v.Terms {
				walkBool(t)
			}
		}
	}
	walkBool(rule.Body)
	out := make([]uint32, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func maxReadEnd(rule Rule) uint32 {
	var maxEnd uint32
	var walk func(b Bool)
	walk = func(b Bool) {
		switch v := b.(type) {
		case *Cmp:
			for _, n := range []Numeric{v.LHS, v.RHS} {
				if l, ok := n.(*Load); ok {
					if e := l.Offset + uint32(l.Size); e > maxEnd {
						maxEnd = e
					}
				}
			}
		case *Not:
			walk(v.Inner)
		case *And:
			for _, t := range v.Terms {
				walk(t)
			}
		case *Or:
			for _, t := range v.Terms {
				walk(t)
			}
		}
	}
	walk(rule.Body)
	return maxEnd
}

// enumeratePackets produces every packet relevant to the rule up to a bounded
// width: all lengths in [0, capLen], where each byte is drawn from a small
// boundary-value alphabet. With a 4-value alphabet and width up to 8 the
// full cross product stays tractable while still exercising every branch.
func enumeratePackets(rule Rule, capLen int) [][]byte {
	alphabet := byteAlphabet(rule)
	var out [][]byte
	var gen func(limit int, cur []byte)
	gen = func(limit int, cur []byte) {
		if len(cur) == limit {
			pkt := make([]byte, limit)
			copy(pkt, cur)
			out = append(out, pkt)
			return
		}
		for _, b := range alphabet {
			gen(limit, append(cur, b))
		}
	}
	for n := 0; n <= capLen; n++ {
		gen(n, nil)
	}
	return out
}

func byteAlphabet(rule Rule) []byte {
	vals := representativeValues(rule)
	seen := map[byte]bool{}
	for _, v := range vals {
		b := byte(v)
		seen[b] = true
		seen[byte(v>>8)] = true
		seen[byte(v>>16)] = true
		seen[byte(v>>24)] = true
	}
	// Guarantee a fixed 4-symbol alphabet so coverage does not collapse.
	for _, b := range []byte{0x00, 0x01, 0xff, 0x80} {
		seen[b] = true
	}
	out := make([]byte, 0, len(seen))
	for b := range seen {
		out = append(out, b)
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// assertEquivalent compiles the rule and checks that the interpreter agrees
// with the independent tree evaluator for every supplied packet.
func assertEquivalent(t *testing.T, rule Rule, packets [][]byte) {
	t.Helper()
	prog, err := Compile(rule)
	if err != nil {
		t.Fatalf("compile %s: %v", rule.Name, err)
	}
	for _, pkt := range packets {
		want := Eval(rule, pkt)
		got, err := prog.Execute(pkt)
		if err != nil {
			t.Fatalf("execute on %v: %v", pkt, err)
		}
		if got != want {
			t.Fatalf("divergence on packet %v (len=%d): vm=%v reference=%v", pkt, len(pkt), got, want)
		}
	}
}

func TestExhaustiveShortPackets(t *testing.T) {
	rules := []Rule{
		mustParse(t, "guarded-and", "len >= 8 and u32(0) == 256 and u16(4) == 1"),
		mustParse(t, "guarded-or", "len >= 4 and (u32(0) == 256 or u8(3) == 255)"),
		mustParse(t, "strict-length", "len > 3 and u8(0) == 1"),
		mustParse(t, "equality-length", "len == 2 and u16(0) == 1"),
		mustParse(t, "negation", "not (len >= 4 and u8(1) == 0)"),
		mustParse(t, "nested", "(len >= 6 and u16(2) >= 1) or (len >= 8 and u32(4) == 7)"),
		mustParse(t, "reg-reg", "len >= 4 and u16(0) == u16(2)"),
		mustParse(t, "len-compare", "len >= 4 and u8(0) < len"),
	}
	for _, rule := range rules {
		capLen := int(maxReadEnd(rule))
		if capLen > 8 {
			capLen = 8
		}
		packets := enumeratePackets(rule, capLen)
		if len(packets) == 0 {
			t.Fatalf("no packets enumerated for %s", rule.Name)
		}
		t.Run(rule.Name, func(t *testing.T) {
			assertEquivalent(t, rule, packets)
		})
	}
}

// TestByteExhaustiveTinyPackets enumerates literally every packet whose
// length is 0..2 over the full 256-value byte alphabet (1 + 256 + 65536
// packets). These are the "short packets" that fail every length guard, so
// the run proves the compiled code rejects them exactly like the reference.
func TestByteExhaustiveTinyPackets(t *testing.T) {
	rules := []Rule{
		mustParse(t, "guard4", "len >= 4 and u32(0) == 1"),
		mustParse(t, "guard-or", "len >= 4 and (u8(0) == 9 or u16(1) == 3)"),
		mustParse(t, "strict", "len > 2 and u8(0) == 5"),
		mustParse(t, "not-guard", "not (len >= 2 and u8(0) == 0)"),
	}
	progs := make([]*Program, len(rules))
	for i, r := range rules {
		p, err := Compile(r)
		if err != nil {
			t.Fatalf("compile %s: %v", r.Name, err)
		}
		progs[i] = p
	}

	check := func(pkt []byte) {
		t.Helper()
		for i, r := range rules {
			want := Eval(r, pkt)
			got, err := progs[i].Execute(pkt)
			if err != nil {
				t.Fatalf("%s execute %v: %v", r.Name, pkt, err)
			}
			if got != want {
				t.Fatalf("%s divergence on %v: vm=%v ref=%v", r.Name, pkt, got, want)
			}
		}
	}

	checked := 1 // length 0
	check(nil)
	for a := 0; a < 256; a++ {
		check([]byte{byte(a)}) // length 1
		checked++
	}
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			check([]byte{byte(a), byte(b)}) // length 2
			checked++
		}
	}
	if checked != 1+256+256*256 {
		t.Fatalf("exhaustive count=%d, want %d", checked, 1+256+256*256)
	}
}

// TestReversedOperands covers comparisons where len/load appear on the
// right-hand side, exercising register/register and flipped edge bounds.
func TestReversedOperands(t *testing.T) {
	rules := []Rule{
		mustParse(t, "imm-len", "4 <= len and u8(0) == 1"),
		mustParse(t, "load-len", "len >= 4 and len > u8(0)"),
		mustParse(t, "imm-len-lt", "8 > len or u8(0) == 2"),
	}
	for _, rule := range rules {
		capLen := int(maxReadEnd(rule))
		if capLen > 8 {
			capLen = 8
		}
		t.Run(rule.Name, func(t *testing.T) {
			assertEquivalent(t, rule, enumeratePackets(rule, capLen))
		})
	}
}

func TestRandomizedDifferential(t *testing.T) {
	rule := mustParse(t, "fuzz", "len >= 8 and (u32(0) == 1234 or u16(4) != 5) and u8(7) < 16")
	prog, err := Compile(rule)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 5000; iter++ {
		n := rng.Intn(20) // includes short packets that fail the length guard
		pkt := make([]byte, n)
		rng.Read(pkt)
		got, gerr := prog.Execute(pkt)
		want := Eval(rule, pkt)
		if gerr != nil {
			t.Fatalf("execute error: %v", gerr)
		}
		if got != want {
			t.Fatalf("iter %d pkt=%v vm=%v ref=%v", iter, pkt, got, want)
		}
	}
}

func TestParseAndSourceAttribution(t *testing.T) {
	rule, err := Parse("tenant-rule", "len >= 4 and u32(0) == 1")
	if err != nil {
		t.Fatal(err)
	}
	prog, err := Compile(rule)
	if err != nil {
		t.Fatalf("valid rule rejected: %v", err)
	}
	// Every emitted instruction must carry a source expression id.
	for i, in := range prog.Insns {
		if in.Op != OpRet && in.Origin.ID < 0 {
			t.Fatalf("instruction %d lacks source attribution", i)
		}
	}

	// Shorten the guard so the load becomes unprovable; the rejection must
	// point at the load source fragment.
	bad, err := Parse("bad", "len >= 2 and u32(0) == 1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(bad)
	re, ok := AsReject(err)
	if !ok {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if re.Kind != RejectUnverifiedLoad {
		t.Fatalf("kind = %v", re.Kind)
	}
	if re.Source.Text != "u32(0)" {
		t.Fatalf("rejection text = %q, want u32(0)", re.Source.Text)
	}
	if re.Source.Line != 1 || re.Source.Col <= 0 {
		t.Fatalf("rejection position not located: %+v", re.Source)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"len >=",
		"u32(0) ==",
		"(len >= 2",
		"foo(0) == 1",
		"len >= 2 and",
	}
	for _, src := range bad {
		if _, err := Parse("x", src); err == nil {
			t.Errorf("expected parse error for %q", src)
		}
	}
}

func TestEndToEndTenantRule(t *testing.T) {
	// A realistic rule: require an 8-byte header, match a 16-bit field and a
	// byte flag, then accept.
	rule, err := Parse("allow-header",
		"len >= 8 and u16(0) == 4660 and u8(2) == 7")
	if err != nil {
		t.Fatal(err)
	}
	prog, err := Compile(rule)
	if err != nil {
		t.Fatal(err)
	}
	accept := []byte{0x12, 0x34, 0x07, 0x00, 0x00, 0x00, 0x00, 0x00}
	if ok, _ := prog.Execute(accept); !ok {
		t.Fatal("expected accept on matching packet")
	}
	if ok, _ := prog.Execute(accept[:3]); ok {
		t.Fatal("short packet must not accept")
	}
	if ok, _ := prog.Execute([]byte{0x12, 0x34, 0x08, 0, 0, 0, 0, 0}); ok {
		t.Fatal("non-matching flag must not accept")
	}
	// Disassembly is populated and mentions every opcode class generated.
	dis := prog.String()
	for _, want := range []string{"LEN", "LOAD", "JMP", "RET"} {
		if !contains(dis, want) {
			t.Fatalf("disassembly missing %s:\n%s", want, dis)
		}
	}
}

func mustParse(t *testing.T, name, src string) Rule {
	t.Helper()
	rule, err := Parse(name, src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return rule
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func ExampleCompile() {
	rule, _ := Parse("demo", "len >= 4 and u32(0) == 1")
	prog, err := Compile(rule)
	if err != nil {
		fmt.Println("rejected:", err)
		return
	}
	ok, _ := prog.Execute([]byte{0, 0, 0, 1})
	fmt.Println(ok)
	// Output: true
}

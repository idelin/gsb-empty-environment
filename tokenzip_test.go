package tokenzip

import (
	"math/big"
	"reflect"
	"testing"
)

func errKind(err error) ErrKind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return 0
}

// Nested references: e0 = [a b], e1 = [e0 c], e2 = [e1 e1]
// Expansion of e2: a b c a b c
func nestedDict(t *testing.T) *Dictionary {
	t.Helper()
	d, err := Compile([][]Elem{
		{Lit("a"), Lit("b")},
		{Ref(0), Lit("c")},
		{Ref(1), Ref(1)},
	}, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return d
}

func TestNestedLengths(t *testing.T) {
	d := nestedDict(t)
	want := []int64{2, 3, 6}
	for i, w := range want {
		l, err := d.Len(i)
		if err != nil {
			t.Fatalf("Len(%d): %v", i, err)
		}
		if l.Cmp(big.NewInt(w)) != 0 {
			t.Errorf("Len(%d) = %s, want %d", i, l, w)
		}
	}
}

func TestNestedLocateMapsToOriginalPositions(t *testing.T) {
	d := nestedDict(t)
	spans, err := d.Locate(2, big.NewInt(1), big.NewInt(5))
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	var got []string
	for _, s := range spans {
		got = append(got, s.String())
	}
	want := []string{
		`"b" @1 via 2 1 0.1`,
		`"c" @2 via 2 1.1`,
		`"a" @3 via 2 1 0.0`,
		`"b" @4 via 2 1 0.1`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spans:\n got %q\nwant %q", got, want)
	}
}

func TestLocateFullRange(t *testing.T) {
	d := nestedDict(t)
	spans, err := d.Locate(2, big.NewInt(0), big.NewInt(6))
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	var tokens []string
	for i, s := range spans {
		tokens = append(tokens, s.Token)
		if s.Offset.Cmp(big.NewInt(int64(i))) != 0 {
			t.Errorf("span %d offset = %s, want %d", i, s.Offset, i)
		}
	}
	if want := []string{"a", "b", "c", "a", "b", "c"}; !reflect.DeepEqual(tokens, want) {
		t.Errorf("tokens = %v, want %v", tokens, want)
	}
}

func TestEmptyPhraseRejected(t *testing.T) {
	_, err := Compile([][]Elem{{Lit("a")}, {}}, nil)
	if errKind(err) != ErrEmptyPhrase {
		t.Fatalf("kind = %v (%v), want ErrEmptyPhrase", errKind(err), err)
	}
	if e := err.(*Error); e.Entry != 1 {
		t.Errorf("entry = %d, want 1", e.Entry)
	}
}

func TestBadReference(t *testing.T) {
	for _, entries := range [][][]Elem{
		{{Ref(7)}},
		{{Lit("a")}, {Ref(-1)}},
	} {
		if _, err := Compile(entries, nil); errKind(err) != ErrBadReference {
			t.Errorf("Compile(%v): kind = %v (%v), want ErrBadReference",
				entries, errKind(err), err)
		}
	}
}

func TestCycleDetected(t *testing.T) {
	cases := [][][]Elem{
		{{Ref(0)}},                     // self reference
		{{Ref(1)}, {Ref(0)}},           // mutual
		{{Ref(2)}, {Ref(0)}, {Ref(1)}}, // 0 -> 2 -> 1 -> 0
	}
	for i, entries := range cases {
		if _, err := Compile(entries, nil); errKind(err) != ErrCycle {
			t.Errorf("case %d: kind = %v (%v), want ErrCycle", i, errKind(err), err)
		}
	}
}

func TestBudgetExceeded(t *testing.T) {
	// Doubling chain: entry i has length 2^i.
	entries := [][]Elem{{Lit("x")}}
	for i := 1; i <= 10; i++ {
		entries = append(entries, []Elem{Ref(i - 1), Ref(i - 1)})
	}
	if _, err := Compile(entries, big.NewInt(1000)); errKind(err) != ErrBudgetExceeded {
		t.Fatalf("kind = %v (%v), want ErrBudgetExceeded", errKind(err), err)
	}
	// Same chain within budget compiles fine.
	if _, err := Compile(entries, big.NewInt(1024)); err != nil {
		t.Fatalf("Compile within budget: %v", err)
	}
}

func TestHugeExpansionShortInterval(t *testing.T) {
	// e0 = [a b]; e_i = [e_{i-1} e_{i-1}] => length 2^(i+1), tokens alternate.
	const depth = 200
	entries := [][]Elem{{Lit("a"), Lit("b")}}
	for i := 1; i <= depth; i++ {
		entries = append(entries, []Elem{Ref(i - 1), Ref(i - 1)})
	}
	d, err := Compile(entries, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Length of e_depth is 2^(depth+1): far beyond int64.
	total, err := d.Len(depth)
	if err != nil {
		t.Fatalf("Len: %v", err)
	}
	want := new(big.Int).Lsh(big.NewInt(1), depth+1)
	if total.Cmp(want) != 0 {
		t.Fatalf("Len = %s, want %s", total, want)
	}

	// A short interval straddling the exact midpoint 2^depth.
	mid := new(big.Int).Lsh(big.NewInt(1), depth)
	start := new(big.Int).Sub(mid, big.NewInt(2))
	end := new(big.Int).Add(mid, big.NewInt(2))
	spans, err := d.Locate(depth, start, end)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if len(spans) != 4 {
		t.Fatalf("got %d spans, want 4", len(spans))
	}
	for i, s := range spans {
		wantOff := new(big.Int).Add(start, big.NewInt(int64(i)))
		if s.Offset.Cmp(wantOff) != 0 {
			t.Errorf("span %d offset = %s, want %s", i, s.Offset, wantOff)
		}
		// Tokens alternate a,b starting with a at offset 0.
		wantTok := "a"
		if wantOff.Bit(0) == 1 {
			wantTok = "b"
		}
		if s.Token != wantTok {
			t.Errorf("span %d token = %q, want %q", i, s.Token, wantTok)
		}
		// Path must descend from e_depth all the way to e0.
		if len(s.Path) != depth+1 {
			t.Fatalf("span %d path length = %d, want %d", i, len(s.Path), depth+1)
		}
		for j, p := range s.Path {
			if p != depth-j {
				t.Errorf("span %d path[%d] = %d, want %d", i, j, p, depth-j)
			}
		}
		if s.Elem != int(wantOff.Bit(0)) {
			t.Errorf("span %d elem = %d, want %d", i, s.Elem, wantOff.Bit(0))
		}
	}
}

func TestBadInterval(t *testing.T) {
	d := nestedDict(t)
	cases := [][2]int64{{-1, 2}, {3, 2}, {0, 7}}
	for _, c := range cases {
		_, err := d.Locate(2, big.NewInt(c[0]), big.NewInt(c[1]))
		if errKind(err) != ErrBadInterval {
			t.Errorf("interval %v: kind = %v (%v), want ErrBadInterval", c, errKind(err), err)
		}
	}
	if _, err := d.Locate(9, big.NewInt(0), big.NewInt(1)); errKind(err) != ErrBadReference {
		t.Errorf("bad entry: kind = %v, want ErrBadReference", errKind(err))
	}
}

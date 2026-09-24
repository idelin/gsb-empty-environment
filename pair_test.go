package canonkey

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strconv"
	"testing"
)

// equivalent is an independent semantic-equality oracle: object field
// order and number spelling are insignificant; everything else matters.
func equivalent(a, b any) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok {
			return false
		}
		ra, ok1 := new(big.Rat).SetString(string(av))
		rb, ok2 := new(big.Rat).SetString(string(bv))
		return ok1 && ok2 && ra.Cmp(rb) == 0
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equivalent(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, va := range av {
			vb, ok := bv[k]
			if !ok || !equivalent(va, vb) {
				return false
			}
		}
		return true
	default:
		panic(fmt.Sprintf("oracle: unsupported %T", a))
	}
}

var sampleStrings = []string{"", "a", "b", "1", "null", "true", "café", "café", "0"}

func randValue(rng *rand.Rand, depth int) any {
	if depth > 4 {
		return json.Number(strconv.Itoa(rng.Intn(4)))
	}
	switch rng.Intn(9) {
	case 0:
		return nil
	case 1:
		return rng.Intn(2) == 0
	case 2:
		return json.Number(strconv.Itoa(rng.Intn(4)))
	case 3:
		return json.Number(fmt.Sprintf("%d.%d", rng.Intn(3), rng.Intn(10)))
	case 4:
		return sampleStrings[rng.Intn(len(sampleStrings))]
	case 5, 6:
		n := rng.Intn(3)
		arr := make([]any, n)
		for i := range arr {
			arr[i] = randValue(rng, depth+1)
		}
		return arr
	default:
		n := rng.Intn(3)
		m := map[string]any{}
		keys := []string{"a", "b", "c"}
		rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		for i := 0; i < n; i++ {
			m[keys[i]] = randValue(rng, depth+1)
		}
		return m
	}
}

// mutate returns a paired input: sometimes semantically equivalent
// (key order, number respelling), sometimes deliberately different.
func mutate(rng *rand.Rand, v any) any {
	switch t := v.(type) {
	case map[string]any:
		// Rebuilding a map reorders iteration: equivalent by construction.
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = val
		}
		if rng.Intn(2) == 0 && len(out) > 0 {
			for k := range out { // non-equivalent: tweak one value
				out[k] = mutateLeaf(rng, out[k])
				break
			}
		}
		return out
	case []any:
		out := append([]any(nil), t...)
		if len(out) > 1 && rng.Intn(2) == 0 {
			out[0], out[1] = out[1], out[0] // non-equivalent: order matters
		}
		return out
	default:
		return mutateLeaf(rng, v)
	}
}

func mutateLeaf(rng *rand.Rand, v any) any {
	switch t := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err == nil && f == float64(int64(f)) && rng.Intn(2) == 0 {
			return json.Number(fmt.Sprintf("%d.0", int64(f))) // equivalent respelling
		}
		return json.Number(string(t) + "1") // non-equivalent
	case string:
		return t + "x" // non-equivalent
	case bool:
		return !t // non-equivalent
	case nil:
		return json.Number("0") // non-equivalent
	default:
		return v
	}
}

// TestPairedCounterexampleSearch generates paired inputs and fails on any
// non-equivalent pair that shares a key, or any equivalent pair that
// does not.
func TestPairedCounterexampleSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(20260924))
	lim := DefaultLimits()
	for i := 0; i < 20000; i++ {
		a := randValue(rng, 0)
		b := mutate(rng, a)
		ca, err := Canonical(a, lim)
		if err != nil {
			t.Fatalf("canonicalize a: %v", err)
		}
		cb, err := Canonical(b, lim)
		if err != nil {
			t.Fatalf("canonicalize b: %v", err)
		}
		sameKey := bytes.Equal(ca, cb)
		sameSem := equivalent(a, b)
		if sameKey != sameSem {
			t.Fatalf("counterexample at iter %d: sameKey=%v sameSem=%v\na=%#v\nb=%#v\nca=%s\ncb=%s",
				i, sameKey, sameSem, a, b, ca, cb)
		}
	}
}

// TestPairedDistinctCorpus checks a corpus of pairwise non-equivalent
// values produces pairwise distinct keys.
func TestPairedDistinctCorpus(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	seen := map[string]any{}
	for i := 0; i < 5000; i++ {
		v := randValue(rng, 0)
		c, err := Canonical(v, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := seen[string(c)]; dup && !equivalent(prev, v) {
			t.Fatalf("non-equivalent values share a key:\n%#v\n%#v\n%s", prev, v, c)
		}
		seen[string(c)] = v
	}
}

// FuzzCanonicalPair extends the paired search to arbitrary JSON inputs.
func FuzzCanonicalPair(f *testing.F) {
	f.Add(`{"a":1,"b":[1,2]}`, `{"b":[1,2],"a":1.0}`)
	f.Add(`{"x":null}`, `{}`)
	f.Add(`"1"`, `1`)
	f.Fuzz(func(t *testing.T, s1, s2 string) {
		a, err1 := ParseParams(bytes.NewReader([]byte(s1)), Limits{})
		b, err2 := ParseParams(bytes.NewReader([]byte(s2)), Limits{})
		if err1 != nil || err2 != nil {
			return
		}
		ca, err := Canonical(a, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		cb, err := Canonical(b, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(ca, cb) != equivalent(a, b) {
			t.Fatalf("counterexample:\na=%#v\nb=%#v\nca=%s\ncb=%s", a, b, ca, cb)
		}
	})
}

// TestParseParamsNullVsAbsent ensures the parser preserves the
// null-vs-absent distinction at the envelope level.
func TestParseParamsNullVsAbsent(t *testing.T) {
	v, err := ParseParams(bytes.NewReader([]byte("null")), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if v != Null {
		t.Fatalf("top-level null must decode to Null sentinel, got %#v", v)
	}
	k := NewKeyer(Limits{})
	kNull := k.MustBuildKey(Request{Tenant: "t", ConfigVersion: "c", Params: v})
	kAbsent := k.MustBuildKey(Request{Tenant: "t", ConfigVersion: "c", Params: nil})
	if kNull.Digest == kAbsent.Digest {
		t.Fatal("null params and absent params must not share a key")
	}
}

// TestSortedKeyDeterminism pins the exact canonical bytes of a nested
// document so any drift in ordering or framing is caught.
func TestSortedKeyDeterminism(t *testing.T) {
	v := map[string]any{
		"b": []any{json.Number("2"), nil, true},
		"a": map[string]any{"y": "z"},
	}
	got := canon(t, v)
	want := `o2{s1:ao1{s1:ys1:z}s1:ba3[n2e0zbt]}`
	if got != want {
		t.Fatalf("canonical form drifted:\ngot  %q\nwant %q", got, want)
	}
	// Sorting is by raw bytes, independent of insertion order.
	keys := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		keys = append(keys, fmt.Sprintf("k%02d", i))
	}
	rng := rand.New(rand.NewSource(1))
	rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	m := map[string]any{}
	for _, k := range keys {
		m[k] = json.Number("1")
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	want = "o50{"
	for _, k := range sorted {
		want += "s3:" + k + "n1e0"
	}
	want += "}"
	if got := canon(t, m); got != want {
		t.Fatal("field sorting is not deterministic byte order")
	}
}

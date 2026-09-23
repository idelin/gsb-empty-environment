package objectgraph

import (
	"testing"
)

func FuzzDecode(f *testing.F) {
	// Seed with a few valid and hostile byte strings.
	b := NewBuilder()
	seed, err := b.Encode(b.AddObject([]KV{{Key: "k", Ref: b.AddArray([]int{b.AddInt(1), b.AddString("s")})}}))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{'O', 'G', '1', 1, 1, 0, tagNull})
	f.Add([]byte{0xff, 0xff})

	d, err := NewDecoder(DefaultLimits())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, p []byte) {
		v, err := d.Decode(p)
		if err != nil && v != nil {
			t.Fatalf("non-nil partial object returned with error %v", err)
		}
		if v == nil {
			return
		}
		// Successful graphs must be well-formed: traverse a bounded number of
		// edges using pointer-identity visited set, never trusting recursion.
		seen := map[*Value]bool{}
		stack := []*Value{v}
		steps := 0
		for len(stack) > 0 && steps < 1<<20 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if cur == nil || seen[cur] {
				continue
			}
			seen[cur] = true
			steps++
			stack = append(stack, cur.Elems()...)
			for _, fld := range cur.Fields() {
				stack = append(stack, fld.Value)
			}
		}
	})
}

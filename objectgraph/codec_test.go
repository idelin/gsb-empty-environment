package objectgraph

import (
	"testing"
)

func mustEnc(t *testing.T, b *Builder, root int) []byte {
	t.Helper()
	p, err := b.Encode(root)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return p
}

func TestScalarRoundTrip(t *testing.T) {
	type tc struct {
		name string
		enc  func(b *Builder) int
		chk  func(t *testing.T, v *Value)
	}
	cases := []tc{
		{"null", func(b *Builder) int { return b.AddNull() }, func(t *testing.T, v *Value) {
			if v.Kind() != KindNull {
				t.Fatalf("kind %v", v.Kind())
			}
		}},
		{"bool", func(b *Builder) int { return b.AddBool(true) }, func(t *testing.T, v *Value) {
			if !v.Bool() {
				t.Fatal("want true")
			}
		}},
		{"int", func(b *Builder) int { return b.AddInt(-42) }, func(t *testing.T, v *Value) {
			if v.Int() != -42 {
				t.Fatalf("int %d", v.Int())
			}
		}},
		{"float", func(b *Builder) int { return b.AddFloat(2.5) }, func(t *testing.T, v *Value) {
			if v.Float() != 2.5 {
				t.Fatalf("float %v", v.Float())
			}
		}},
		{"string", func(b *Builder) int { return b.AddString("héllo") }, func(t *testing.T, v *Value) {
			if v.Text() != "héllo" {
				t.Fatalf("text %q", v.Text())
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := NewBuilder()
			root := c.enc(b)
			v, err := Decode(mustEnc(t, b, root))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			c.chk(t, v)
		})
	}
}

func TestArrayObjectAndSharedNode(t *testing.T) {
	b := NewBuilder()
	n1 := b.AddString("a")
	n2 := b.AddInt(1)
	arr := b.AddArray([]int{n1, n2, n1})
	obj := b.AddObject([]KV{{Key: "x", Ref: arr}, {Key: "y", Ref: n2}})
	v, err := Decode(mustEnc(t, b, obj))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.Kind() != KindObject || v.Len() != 2 {
		t.Fatalf("root kind=%v len=%d", v.Kind(), v.Len())
	}
	got := v.Field("x")
	if got == nil || got.Kind() != KindArray || got.Len() != 3 {
		t.Fatalf("x field wrong: %+v", got)
	}
	if got.Elems()[0] != got.Elems()[2] {
		t.Fatal("shared node n1 must be a single entity (pointer identity)")
	}
	if got.Elems()[0].Text() != "a" || got.Elems()[1].Int() != 1 {
		t.Fatal("array element values wrong")
	}
	if v.Field("y").Int() != 1 {
		t.Fatal("y field wrong")
	}
}

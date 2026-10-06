package viewbuilder

import (
	"bytes"
	"fmt"
)

// Checked wraps a Builder and verifies every public query against a full
// recompute of the raw log at the view's own sequence. Any divergence
// between the incrementally maintained view and the from-scratch state
// is reported through Fatalf.
type Checked struct {
	B      *Builder
	L      *Log
	Fatalf func(format string, args ...any)
}

func NewChecked(b *Builder, l *Log, fatalf func(string, ...any)) *Checked {
	return &Checked{B: b, L: l, Fatalf: fatalf}
}

func (c *Checked) failf(format string, args ...any) {
	if c.Fatalf != nil {
		c.Fatalf(format, args...)
		return
	}
	panic(fmt.Sprintf(format, args...))
}

// Get queries the live view and compares the result with a full
// recompute at the view's sequence.
func (c *Checked) Get(key string) ([]byte, bool) {
	v := c.B.View()
	got, ok := v.Get(key)
	want, wok := c.L.recompute(v.Seq())[key]
	if ok != wok || !bytes.Equal(got, want) {
		c.failf("Get(%q) diverged at seq %d: view=(%q,%v) recompute=(%q,%v)",
			key, v.Seq(), got, ok, want, wok)
	}
	return got, ok
}

// Len queries the live view and compares it with a full recompute.
func (c *Checked) Len() int {
	v := c.B.View()
	want := len(c.L.recompute(v.Seq()))
	if v.Len() != want {
		c.failf("Len diverged at seq %d: view=%d recompute=%d", v.Seq(), v.Len(), want)
	}
	return v.Len()
}

// CheckAll compares the entire current view, key by key, against a full
// recompute at the view's sequence.
func (c *Checked) CheckAll() {
	v := c.B.View()
	want := c.L.recompute(v.Seq())
	if v.Len() != len(want) {
		c.failf("CheckAll diverged at seq %d: view has %d keys, recompute has %d",
			v.Seq(), v.Len(), len(want))
	}
	for key, wv := range want {
		got, ok := v.Get(key)
		if !ok || !bytes.Equal(got, wv) {
			c.failf("CheckAll diverged at seq %d: key %q view=(%q,%v) recompute=(%q,true)",
				v.Seq(), key, got, ok, wv)
		}
	}
}

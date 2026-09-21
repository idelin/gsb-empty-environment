package bitlayout_test

import (
	"testing"

	"bitlayout"
)

const nestedSchema = `{
  "name": "nested", "bitOrder": "msb", "byteOrder": "big",
  "fields": [
    { "kind": "int", "name": "rows", "bits": 2 },
    { "kind": "array", "name": "matrix", "lengthFrom": "rows",
      "element": { "kind": "struct", "name": "row", "fields": [
        { "kind": "int", "name": "cols", "bits": 2 },
        { "kind": "array", "name": "cells", "lengthFrom": "cols",
          "element": { "kind": "int", "bits": 5 } }
      ] } }
  ]
}`

func nestedRecord(rows, cols int) map[string]any {
	m := make([]any, rows)
	for i := range m {
		cells := make([]any, cols)
		for j := range cells {
			cells[j] = uint64(j)
		}
		m[i] = map[string]any{"cols": uint64(cols), "cells": cells}
	}
	return map[string]any{"rows": uint64(rows), "matrix": m}
}

func encodeNested(t *testing.T, plan *bitlayout.Plan, rec map[string]any) ([]byte, int64) {
	t.Helper()
	nbits, err := plan.Validate(rec)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, (nbits+7)/8)
	if _, err := plan.Encode(rec, buf); err != nil {
		t.Fatal(err)
	}
	return buf, nbits
}

func TestQuotaElements(t *testing.T) {
	plan := mustCompile(t, nestedSchema)
	buf, nbits := encodeNested(t, plan, nestedRecord(3, 3)) // 3+9 = 12 elements
	_, _, err := plan.Decode(buf, nbits, &bitlayout.DecodeOptions{MaxTotalElements: 5})
	e, ok := err.(*bitlayout.Error)
	if !ok || e.Kind != bitlayout.ErrQuota {
		t.Fatalf("want quota error, got %v", err)
	}
	if _, _, err := plan.Decode(buf, nbits, &bitlayout.DecodeOptions{MaxTotalElements: 12}); err != nil {
		t.Fatalf("12 elements should fit quota 12: %v", err)
	}
}

func TestQuotaBits(t *testing.T) {
	plan := mustCompile(t, nestedSchema)
	buf, nbits := encodeNested(t, plan, nestedRecord(2, 2))
	if _, _, err := plan.Decode(buf, nbits, &bitlayout.DecodeOptions{MaxTotalBits: nbits - 1}); err == nil {
		t.Fatal("want bit quota error")
	}
	if _, _, err := plan.Decode(buf, nbits, &bitlayout.DecodeOptions{MaxTotalBits: nbits}); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaDepth(t *testing.T) {
	plan := mustCompile(t, nestedSchema)
	buf, nbits := encodeNested(t, plan, nestedRecord(1, 1))
	if _, _, err := plan.Decode(buf, nbits, &bitlayout.DecodeOptions{MaxDepth: 1}); err == nil {
		t.Fatal("want depth quota error")
	}
	if _, _, err := plan.Decode(buf, nbits, &bitlayout.DecodeOptions{MaxDepth: 8}); err != nil {
		t.Fatal(err)
	}
}

// TestSessionIsolation: two sessions on the same plan keep independent
// cursors and quota counters.
func TestSessionIsolation(t *testing.T) {
	plan, buf, nbits, _ := encodeSensor(t)
	s1 := plan.NewSession(&bitlayout.DecodeOptions{MaxTotalElements: 100})
	s2 := plan.NewSession(&bitlayout.DecodeOptions{MaxTotalElements: 1})
	half := nbits / 2
	s1.Feed(buf[:(half+7)/8], half)
	s2.Feed(buf[:(half+7)/8], half)
	if _, done, _ := s1.Try(); done {
		t.Fatal("s1 decoded from half input")
	}
	s1.Feed(buf[half/8:], nbits-half)
	if _, done, err := s1.Try(); !done || err != nil {
		t.Fatalf("s1: done=%v err=%v", done, err)
	}
	s2.Feed(buf[half/8:], nbits-half)
	// s2's tiny element quota must reject the 3-sample record
	if _, _, err := s2.Try(); err == nil {
		t.Fatal("s2 should hit its own quota")
	} else if e := err.(*bitlayout.Error); e.Kind != bitlayout.ErrQuota {
		t.Fatalf("s2: want quota error, got %v", err)
	}
	// s1's successful result is unaffected by s2's failure
	if _, done, err := s1.Try(); !done || err != nil {
		t.Fatalf("s1 polluted by s2: done=%v err=%v", done, err)
	}
}

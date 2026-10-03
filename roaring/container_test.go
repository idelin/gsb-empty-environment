package roaring

import (
	"math/rand"
	"sort"
	"testing"
)

func collect(c container) []uint16 {
	var out []uint16
	c.forEach(func(x uint16) { out = append(out, x) })
	return out
}

func buildContainer(t containerType, vals []uint16) container {
	sorted := append([]uint16(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	uniq := sorted[:0]
	for i, v := range sorted {
		if i == 0 || v != sorted[i-1] {
			uniq = append(uniq, v)
		}
	}
	switch t {
	case typeArray:
		return &arrayContainer{content: uniq}
	case typeBitmap:
		bc := newBitmapContainer()
		for _, v := range uniq {
			bc.set(v)
		}
		return bc
	case typeRun:
		rc := &runContainer{}
		for _, v := range uniq {
			rc.add(v)
		}
		return rc
	}
	panic("bad type")
}

func containerSetEqual(c container, want map[uint16]bool) bool {
	if c.cardinality() != len(want) {
		return false
	}
	for v := range want {
		if !c.contains(v) {
			return false
		}
	}
	got := collect(c)
	if len(got) != len(want) {
		return false
	}
	for _, v := range got {
		if !want[v] {
			return false
		}
	}
	return true
}

func TestArrayContainerBasic(t *testing.T) {
	ac := newArrayContainer()
	for i := 0; i < 100; i++ {
		c, changed := ac.add(uint16(i * 3))
		if !changed {
			t.Fatalf("add %d not reported as change", i*3)
		}
		ac = c.(*arrayContainer)
	}
	if ac.cardinality() != 100 {
		t.Fatalf("cardinality = %d, want 100", ac.cardinality())
	}
	for i := 0; i < 100; i++ {
		if !ac.contains(uint16(i * 3)) {
			t.Fatalf("missing %d", i*3)
		}
	}
	if _, changed := ac.add(0); changed {
		t.Fatal("duplicate add reported as change")
	}
	if _, ok := ac.remove(1); ok {
		t.Fatal("remove of absent value reported as change")
	}
	if _, ok := ac.remove(3); !ok {
		t.Fatal("remove of present value not reported")
	}
	if err := ac.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestArrayContainerConvertsAtThreshold(t *testing.T) {
	var c container = newArrayContainer()
	for i := 0; i <= arrayDefaultMaxSize; i++ {
		c, _ = c.add(uint16(i))
	}
	if c.ctype() != typeBitmap {
		t.Fatalf("type = %s, want bitmap after exceeding %d", c.ctype(), arrayDefaultMaxSize)
	}
	if c.cardinality() != arrayDefaultMaxSize+1 {
		t.Fatalf("cardinality = %d", c.cardinality())
	}
}

func TestBitmapContainerBasic(t *testing.T) {
	bc := newBitmapContainer()
	bc.addRange(10, 20)
	if bc.cardinality() != 11 {
		t.Fatalf("cardinality = %d, want 11", bc.cardinality())
	}
	for v := uint16(10); v <= 20; v++ {
		if !bc.contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	bc.removeRange(12, 15)
	if bc.cardinality() != 7 {
		t.Fatalf("cardinality = %d, want 7", bc.cardinality())
	}
	if bc.contains(13) {
		t.Fatal("13 should be removed")
	}
	if err := bc.validate(); err != nil {
		t.Fatal(err)
	}
	// Full-range add/remove across word boundaries.
	bc2 := newBitmapContainer()
	bc2.addRange(0, 65535)
	if bc2.cardinality() != 65536 {
		t.Fatalf("cardinality = %d, want 65536", bc2.cardinality())
	}
	bc2.removeRange(0, 65535)
	if bc2.cardinality() != 0 {
		t.Fatalf("cardinality = %d, want 0", bc2.cardinality())
	}
	if err := bc2.validate(); err == nil {
		t.Fatal("empty bitmap container should fail validation")
	}
}

func TestRunContainerBasic(t *testing.T) {
	rc := &runContainer{}
	// Build [5,7] and [10,11] then bridge them with 8,9.
	for _, v := range []uint16{5, 6, 7, 10, 11, 8, 9} {
		rc.add(v)
	}
	if len(rc.runs) != 1 || rc.runs[0].start != 5 || rc.runs[0].length != 6 {
		t.Fatalf("runs = %+v, want single run [5,11]", rc.runs)
	}
	// Split the run.
	rc.remove(8)
	if len(rc.runs) != 2 {
		t.Fatalf("runs = %+v, want two runs", rc.runs)
	}
	if rc.contains(8) || !rc.contains(7) || !rc.contains(9) {
		t.Fatal("wrong contents after split")
	}
	// Remove edge values.
	rc.remove(5)
	rc.remove(11)
	if rc.contains(5) || rc.contains(11) {
		t.Fatal("edge removal failed")
	}
	if err := rc.validate(); err != nil {
		t.Fatal(err)
	}
	// Remove everything.
	for _, v := range []uint16{6, 7, 9, 10} {
		rc.remove(v)
	}
	if rc.cardinality() != 0 || len(rc.runs) != 0 {
		t.Fatalf("runs = %+v, want empty", rc.runs)
	}
}

func TestRunContainerRangeOps(t *testing.T) {
	rc := &runContainer{}
	rc.addRange(100, 200)
	rc.addRange(300, 400)
	rc.addRange(150, 350) // bridges both runs
	if len(rc.runs) != 1 || rc.runs[0].start != 100 || runEnd(rc.runs[0]) != 400 {
		t.Fatalf("runs = %+v, want single run [100,400]", rc.runs)
	}
	rc.removeRange(150, 250)
	if len(rc.runs) != 2 {
		t.Fatalf("runs = %+v, want two runs", rc.runs)
	}
	if rc.cardinality() != 50+150 {
		t.Fatalf("cardinality = %d", rc.cardinality())
	}
	if err := rc.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestContainerConversions(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		want := map[uint16]bool{}
		var vals []uint16
		for len(vals) < rng.Intn(5000) {
			v := uint16(rng.Intn(65536))
			if !want[v] {
				want[v] = true
				vals = append(vals, v)
			}
		}
		base := buildContainer(typeArray, vals)
		for _, tt := range []containerType{typeArray, typeBitmap, typeRun} {
			c := containerFromType(tt, base)
			if !containerSetEqual(c, want) {
				t.Fatalf("trial %d: conversion to %s lost values", trial, tt)
			}
			if err := c.validate(); err != nil && c.cardinality() > 0 {
				t.Fatalf("trial %d: %s invalid: %v", trial, tt, err)
			}
		}
	}
}

// TestContainerOpsExhaustive cross-checks every container-type pair and
// every set operation against a map-based reference.
func TestContainerOpsExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	types := []containerType{typeArray, typeBitmap, typeRun}
	genSet := func(density string) map[uint16]bool {
		set := map[uint16]bool{}
		switch density {
		case "sparse":
			for i := 0; i < 1+rng.Intn(30); i++ {
				set[uint16(rng.Intn(1024))] = true
			}
		case "dense":
			for i := 0; i < 600; i++ {
				set[uint16(rng.Intn(1024))] = true
			}
		case "runs":
			for r := 0; r < 1+rng.Intn(4); r++ {
				start := rng.Intn(900)
				length := rng.Intn(120)
				for v := start; v < start+length && v < 1024; v++ {
					set[uint16(v)] = true
				}
			}
		}
		return set
	}
	toSlice := func(m map[uint16]bool) []uint16 {
		out := make([]uint16, 0, len(m))
		for v := range m {
			out = append(out, v)
		}
		return out
	}
	ops := map[string]func(a, b container) container{
		"and": intersect,
		"or":  union,
		"xor": xorContainers,
		"dif": diff,
	}
	modelOp := func(op string, a, b map[uint16]bool) map[uint16]bool {
		out := map[uint16]bool{}
		switch op {
		case "and":
			for v := range a {
				if b[v] {
					out[v] = true
				}
			}
		case "or":
			for v := range a {
				out[v] = true
			}
			for v := range b {
				out[v] = true
			}
		case "xor":
			for v := range a {
				if !b[v] {
					out[v] = true
				}
			}
			for v := range b {
				if !a[v] {
					out[v] = true
				}
			}
		case "dif":
			for v := range a {
				if !b[v] {
					out[v] = true
				}
			}
		}
		return out
	}
	for _, densityA := range []string{"sparse", "dense", "runs"} {
		for _, densityB := range []string{"sparse", "dense", "runs"} {
			for trial := 0; trial < 10; trial++ {
				setA := genSet(densityA)
				setB := genSet(densityB)
				for _, ta := range types {
					for _, tb := range types {
						ca := buildContainer(ta, toSlice(setA))
						cb := buildContainer(tb, toSlice(setB))
						for op, fn := range ops {
							got := fn(ca, cb)
							want := modelOp(op, setA, setB)
							if !containerSetEqual(got, want) {
								t.Fatalf("%s %s %s (%s/%s): got %d values, want %d",
									ta, op, tb, densityA, densityB, got.cardinality(), len(want))
							}
						}
					}
				}
			}
		}
	}
}

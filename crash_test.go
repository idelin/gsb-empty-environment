package netsec_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	netsec "example.org/netsec/q0415"
)

func imageKey(img netsec.Image) string {
	blocks := make([]string, 0, len(img))
	for b := range img {
		blocks = append(blocks, b)
	}
	slices.Sort(blocks)
	var sb strings.Builder
	for i, b := range blocks {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%s=%s", b, img[b])
	}
	return sb.String()
}

func imageSet(images []netsec.Image) map[string]netsec.Image {
	set := map[string]netsec.Image{}
	for _, img := range images {
		set[imageKey(img)] = img
	}
	return set
}

// counterRecovery returns a Recovery that rejects any crash image whose
// durable counter drops below the acknowledged lower bound, or whose
// data blocks outlived the counter generation they belong to.
func counterRecovery(acked int, generation int, dataBlocks ...string) netsec.Recovery {
	return func(img netsec.Image) error {
		counter := 0
		if v, ok := img["c"]; ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("counter block corrupt: %v", err)
			}
			counter = n
		}
		if counter < acked {
			return fmt.Errorf("acknowledged counter regressed: %d < %d", counter, acked)
		}
		for _, b := range dataBlocks {
			if _, ok := img[b]; ok && counter < generation {
				return fmt.Errorf("data block %q durable but counter is %d, want >= %d", b, counter, generation)
			}
		}
		return nil
	}
}

// Without a flush barrier the counter bump and the data writes reorder
// freely, so crash images exist where data is durable but the counter
// never advanced.
func TestEnumerateReorderedCounterWrites(t *testing.T) {
	initial := netsec.Image{"c": "05"}
	ops := []netsec.Op{
		netsec.Write("c", "06"),
		netsec.Write("x", "p1"),
		netsec.Write("y", "p2"),
	}
	images, violations, err := netsec.Enumerate(initial, ops, counterRecovery(5, 6, "x", "y"))
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}

	want := []string{
		"c=05", "c=06",
		"c=05,x=p1", "c=06,x=p1",
		"c=05,y=p2", "c=06,y=p2",
		"c=05,x=p1,y=p2", "c=06,x=p1,y=p2",
	}
	got := imageSet(images)
	if len(got) != len(want) {
		t.Fatalf("got %d distinct images, want %d: %v", len(got), len(want), got)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing crash image %q", k)
		}
	}

	if len(violations) != 3 {
		t.Fatalf("got %d violations, want 3: %v", len(violations), violations)
	}
	for _, v := range violations {
		if img := imageKey(v.Image); !strings.HasPrefix(img, "c=05,") {
			t.Errorf("unexpected violating image %q", img)
		}
	}

	// The image missing both data blocks persisted x and y in either
	// order: cross-check against the full permutations of {"x","y"}.
	var multi *netsec.Violation
	for i := range violations {
		if len(violations[i].Image) == 3 {
			multi = &violations[i]
		}
	}
	if multi == nil {
		t.Fatal("no violation with both data blocks durable")
	}
	wantOrders := netsec.Permutations([]string{"x", "y"})
	if !slices.EqualFunc(multi.Orders, wantOrders, slices.Equal) {
		t.Errorf("orders = %v, want full permutation set %v", multi.Orders, wantOrders)
	}
}

// A flush barrier between the counter bump and the data writes makes the
// anomalous images unreachable: every enumerated image keeps the
// acknowledged lower bound.
func TestFlushBarrierProtectsCounter(t *testing.T) {
	initial := netsec.Image{"c": "05"}
	ops := []netsec.Op{
		netsec.Write("c", "06"),
		netsec.Flush(),
		netsec.Write("x", "p1"),
		netsec.Write("y", "p2"),
	}
	images, violations, err := netsec.Enumerate(initial, ops, counterRecovery(5, 6, "x", "y"))
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("barrier should forbid violations, got %v", violations)
	}
	got := imageSet(images)
	for _, k := range []string{"c=05", "c=06", "c=06,x=p1", "c=06,y=p2", "c=06,x=p1,y=p2"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing crash image %q", k)
		}
	}
	if len(got) != 5 {
		t.Errorf("got %d distinct images, want 5: %v", len(got), got)
	}
}

// A forced write lands on the media immediately, so every crash image
// after it carries the counter even though neighbouring cached writes
// may vanish.
func TestForceWriteBypassesReorderWindow(t *testing.T) {
	ops := []netsec.Op{
		netsec.Write("x", "11"),
		netsec.ForceWrite("c", "06"),
		netsec.Write("y", "22"),
	}
	rec := func(img netsec.Image) error {
		if _, ok := img["y"]; ok {
			if _, ok := img["c"]; !ok {
				return fmt.Errorf("data block durable without forced counter")
			}
		}
		return nil
	}
	images, violations, err := netsec.Enumerate(nil, ops, rec)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("forced counter must survive in every image with y, got %v", violations)
	}
	got := imageSet(images)
	want := []string{"", "x=11", "c=06", "c=06,x=11", "c=06,y=22", "c=06,x=11,y=22"}
	if len(got) != len(want) {
		t.Fatalf("got %d distinct images, want %d: %v", len(got), len(want), got)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing crash image %q", k)
		}
	}
}

// Legal persistence orderings respect segment dependencies: a block
// flushed in an earlier segment always lands before later blocks.
func TestViolationOrdersRespectFlushBarrier(t *testing.T) {
	ops := []netsec.Op{
		netsec.Write("a", "1"),
		netsec.Flush(),
		netsec.Write("b", "2"),
	}
	rec := func(img netsec.Image) error {
		if _, ok := img["b"]; ok {
			return fmt.Errorf("flag image containing b")
		}
		return nil
	}
	_, violations, err := netsec.Enumerate(nil, ops, rec)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want 1: %v", len(violations), violations)
	}
	// Only [a b] is legal; [b a] would jump the flush barrier.
	want := [][]string{{"a", "b"}}
	if !slices.EqualFunc(violations[0].Orders, want, slices.Equal) {
		t.Errorf("orders = %v, want %v", violations[0].Orders, want)
	}
	for _, perm := range netsec.Permutations([]string{"a", "b"}) {
		legal := slices.Index(perm, "a") < slices.Index(perm, "b")
		kept := slices.ContainsFunc(violations[0].Orders, func(o []string) bool { return slices.Equal(o, perm) })
		if legal != kept {
			t.Errorf("permutation %v: legal=%v kept=%v", perm, legal, kept)
		}
	}
}

func TestPermutations(t *testing.T) {
	perms := netsec.Permutations([]string{"c", "a", "b"})
	if len(perms) != 6 {
		t.Fatalf("got %d permutations, want 6", len(perms))
	}
	if !slices.Equal(perms[0], []string{"a", "b", "c"}) {
		t.Errorf("first permutation = %v, want [a b c]", perms[0])
	}
	if !slices.Equal(perms[5], []string{"c", "b", "a"}) {
		t.Errorf("last permutation = %v, want [c b a]", perms[5])
	}
	if got := netsec.Permutations(nil); len(got) != 1 || len(got[0]) != 0 {
		t.Errorf("empty set permutations = %v, want [[]]", got)
	}
}

func TestValidation(t *testing.T) {
	rec := func(netsec.Image) error { return nil }

	if _, _, err := netsec.Enumerate(nil, nil, nil); err == nil {
		t.Error("nil recovery callback accepted")
	}

	var many []netsec.Op
	for i := 0; i < netsec.MaxWrites+1; i++ {
		many = append(many, netsec.Write("b", "1"))
	}
	if _, _, err := netsec.Enumerate(nil, many, rec); err == nil {
		t.Error("more than MaxWrites writes accepted")
	}

	ragged := []netsec.Op{netsec.Write("a", "1"), netsec.Write("b", "22")}
	if _, _, err := netsec.Enumerate(nil, ragged, rec); err == nil {
		t.Error("ragged payload lengths accepted")
	}

	upper := []netsec.Op{netsec.Write("A", "1")}
	if _, _, err := netsec.Enumerate(nil, upper, rec); err == nil {
		t.Error("uppercase block name accepted")
	}

	dirty := []netsec.Op{{Kind: netsec.OpFlush, Block: "a"}}
	if _, _, err := netsec.Enumerate(nil, dirty, rec); err == nil {
		t.Error("flush barrier with payload accepted")
	}
}

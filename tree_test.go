package blink

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func keyOf(i int) []byte {
	return []byte(fmt.Sprintf("%08d", i))
}

func valueOf(i int) []byte {
	return []byte(fmt.Sprintf("value-%08d", i))
}

func TestSequentialCRUDAndRange(t *testing.T) {
	tree := New(4)
	for i := 0; i < 500; i++ {
		if !tree.Insert(keyOf(i), valueOf(i)) {
			t.Fatalf("Insert(%d) reported an existing key", i)
		}
	}
	if tree.Len() != 500 {
		t.Fatalf("Len = %d, want 500", tree.Len())
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	for i := 0; i < 500; i++ {
		got, ok := tree.Get(keyOf(i))
		if !ok || !bytes.Equal(got, valueOf(i)) {
			t.Fatalf("Get(%d) = %q, %v", i, got, ok)
		}
	}
	if _, ok := tree.Get(keyOf(999)); ok {
		t.Fatal("unexpected key found")
	}

	if tree.Insert(keyOf(17), []byte("replacement")) {
		t.Fatal("replacement should be reported as an update")
	}
	got, ok := tree.Get(keyOf(17))
	if !ok || !bytes.Equal(got, []byte("replacement")) {
		t.Fatalf("updated value = %q, %v", got, ok)
	}

	entries := tree.Scan(keyOf(10), keyOf(20))
	if len(entries) != 11 {
		t.Fatalf("Scan length = %d, want 11", len(entries))
	}
	for i, entry := range entries {
		want := 10 + i
		if !bytes.Equal(entry.Key, keyOf(want)) {
			t.Fatalf("entry %d key = %q, want %q", i, entry.Key, keyOf(want))
		}
	}

	all := tree.Scan(nil, nil)
	if len(all) != 500 {
		t.Fatalf("full scan length = %d, want 500", len(all))
	}
	for i := 1; i < len(all); i++ {
		if bytes.Compare(all[i-1].Key, all[i].Key) >= 0 {
			t.Fatalf("full scan is not strictly sorted at %d", i)
		}
	}

	if err := tree.Range(keyOf(20), keyOf(10), func(_, _ []byte) bool {
		t.Fatal("callback ran for an invalid range")
		return false
	}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("invalid range error = %v", err)
	}
}

func TestDeleteAndLazyReclaim(t *testing.T) {
	tree := NewWithOptions(Options{MaxKeys: 4, MaxChildren: 4})
	for i := 0; i < 300; i++ {
		tree.Insert(keyOf(i), valueOf(i))
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate before delete: %v", err)
	}

	for i := 0; i < 300; i++ {
		old, ok := tree.Delete(keyOf(i))
		if !ok || !bytes.Equal(old, valueOf(i)) {
			t.Fatalf("Delete(%d) = %q, %v", i, old, ok)
		}
	}
	tree.ReclaimEmpty()
	if tree.Len() != 0 {
		t.Fatalf("Len after deletes = %d, want 0", tree.Len())
	}
	if got := tree.Scan(nil, nil); len(got) != 0 {
		t.Fatalf("scan after deletes returned %d entries", len(got))
	}
	if tree.Stats().Reclaims == 0 {
		t.Fatal("expected at least one empty leaf to be reclaimed")
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate after reclaim: %v", err)
	}

	for i := 0; i < 300; i++ {
		tree.Insert(keyOf(i), valueOf(i+1000))
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate after reinsert: %v", err)
	}
	for i := 0; i < 300; i++ {
		got, ok := tree.Get(keyOf(i))
		if !ok || !bytes.Equal(got, valueOf(i+1000)) {
			t.Fatalf("Get(%d) after reclaim = %q, %v", i, got, ok)
		}
	}
}

func TestMoveRightBeforeParentInsert(t *testing.T) {
	splitReady := make(chan struct{})
	releaseSplit := make(chan struct{})
	tree := NewWithOptions(Options{
		MaxKeys:     4,
		MaxChildren: 4,
		AfterSplit: func() {
			select {
			case <-splitReady:
			default:
				close(splitReady)
			}
			<-releaseSplit
		},
	})

	for i := 0; i < 4; i++ {
		tree.Insert(keyOf(i), valueOf(i))
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tree.Insert(keyOf(4), valueOf(4))
	}()

	<-splitReady
	if got, ok := tree.Get(keyOf(4)); !ok || !bytes.Equal(got, valueOf(4)) {
		t.Fatalf("move-right Get during half split = %q, %v", got, ok)
	}
	if got, ok := tree.Get(keyOf(3)); !ok || !bytes.Equal(got, valueOf(3)) {
		t.Fatalf("left-half Get during half split = %q, %v", got, ok)
	}
	entries := tree.Scan(keyOf(0), keyOf(4))
	if len(entries) != 5 {
		t.Fatalf("scan during half split returned %d entries, want 5", len(entries))
	}
	for i, entry := range entries {
		if !bytes.Equal(entry.Key, keyOf(i)) {
			t.Fatalf("scan entry %d = %q, want %q", i, entry.Key, keyOf(i))
		}
	}

	close(releaseSplit)
	<-done
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate after delayed parent insert: %v", err)
	}
}

func TestRangeBoundariesAreInclusive(t *testing.T) {
	tree := New(3)
	for _, key := range []string{"a", "b", "c", "d", "e", "f"} {
		tree.Insert([]byte(key), []byte("v"+key))
	}

	got := tree.Scan([]byte("c"), []byte("e"))
	if len(got) != 3 ||
		!bytes.Equal(got[0].Key, []byte("c")) ||
		!bytes.Equal(got[1].Key, []byte("d")) ||
		!bytes.Equal(got[2].Key, []byte("e")) {
		t.Fatalf("inclusive scan = %#v", got)
	}

	var visited []string
	err := tree.Range([]byte("d"), nil, func(key, _ []byte) bool {
		visited = append(visited, string(key))
		return len(visited) < 2
	})
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if fmt.Sprint(visited) != "[d e]" {
		t.Fatalf("early-stop range = %v", visited)
	}
}

func TestEmptyKeyAndSliceIsolation(t *testing.T) {
	tree := New(2)
	key := []byte{}
	value := []byte("mutable")
	if !tree.Insert(key, value) {
		t.Fatal("empty key insert was reported as an update")
	}
	tree.Insert([]byte("a"), []byte("va"))
	tree.Insert([]byte("b"), []byte("vb"))
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate with empty-key split: %v", err)
	}
	value[0] = 'X'
	key = append(key, 'x')

	got, ok := tree.Get(nil)
	if !ok || !bytes.Equal(got, []byte("mutable")) {
		t.Fatalf("empty key lookup = %q, %v", got, ok)
	}
	entry := tree.Scan(nil, nil)[0]
	entry.Value[0] = 'Y'
	second, ok := tree.Get(nil)
	if !ok || !bytes.Equal(second, []byte("mutable")) {
		t.Fatalf("returned value aliases internal storage: %q, %v", second, ok)
	}
	if _, ok := tree.Get([]byte{'x'}); ok {
		t.Fatal("caller key mutation changed an indexed key")
	}
	if got := tree.Scan(nil, nil); len(got) != 3 {
		t.Fatalf("scan length = %d, want 3", len(got))
	}
}

func TestRandomizedAgainstMap(t *testing.T) {
	tree := NewWithOptions(Options{MaxKeys: 3, MaxChildren: 3})
	rng := rand.New(rand.NewSource(42))
	model := make(map[string]string)

	for step := 0; step < 6000; step++ {
		n := rng.Intn(500)
		key := keyOf(n)
		ks := string(key)

		switch rng.Intn(10) {
		case 0, 1, 2, 3:
			wantNew := model[ks] == ""
			gotNew := tree.Insert(key, valueOf(step))
			if gotNew != wantNew {
				t.Fatalf("step %d Insert(%d) = %v, want %v", step, n, gotNew, wantNew)
			}
			model[ks] = string(valueOf(step))
		case 4, 5:
			want, wantOK := model[ks]
			got, gotOK := tree.Delete(key)
			if gotOK != wantOK || gotOK && string(got) != want {
				t.Fatalf("step %d Delete(%d) = %q,%v want %q,%v", step, n, got, gotOK, want, wantOK)
			}
			delete(model, ks)
		case 6, 7:
			want, wantOK := model[ks]
			got, gotOK := tree.Get(key)
			if gotOK != wantOK || gotOK && string(got) != want {
				t.Fatalf("step %d Get(%d) = %q,%v want %q,%v", step, n, got, gotOK, want, wantOK)
			}
		default:
			lo := rng.Intn(500)
			hi := lo + rng.Intn(80)
			got := tree.Scan(keyOf(lo), keyOf(hi))
			var want []string
			for k, v := range model {
				if k >= string(keyOf(lo)) && k <= string(keyOf(hi)) {
					want = append(want, k+"="+v)
				}
			}
			sort.Strings(want)
			gotPairs := make([]string, len(got))
			for i, entry := range got {
				gotPairs[i] = string(entry.Key) + "=" + string(entry.Value)
			}
			if fmt.Sprint(gotPairs) != fmt.Sprint(want) {
				t.Fatalf("step %d Scan(%d,%d) mismatch", step, lo, hi)
			}
		}

		if step%250 == 0 {
			if tree.Len() != len(model) {
				t.Fatalf("step %d Len = %d, want %d", step, tree.Len(), len(model))
			}
			if err := tree.Validate(); err != nil {
				t.Fatalf("step %d Validate: %v", step, err)
			}
		}
	}
}

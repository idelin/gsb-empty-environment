package ptrauth

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentFreeRace(t *testing.T) {
	reg := newTestRegistry(t)

	const objects = 32
	const readers = 8
	const iterations = 200

	type item struct {
		obj     *Object
		backing []byte
		ptr     TaggedPtr
	}
	items := make([]item, objects)
	for i := range items {
		backing := make([]byte, 64)
		obj, err := reg.RegisterData(backing)
		if err != nil {
			t.Fatalf("RegisterData %d: %v", i, err)
		}
		ptr, err := reg.Tag(obj, KindReadWrite, ctxA)
		if err != nil {
			t.Fatalf("Tag %d: %v", i, err)
		}
		items[i] = item{obj: obj, backing: backing, ptr: ptr}
	}

	var allowed atomic.Int64
	var denied atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Reader goroutines hammer the dereference gate. Each outcome must be
	// either a fully successful access or a classified denial; a panic or a
	// read of detached memory would indicate a torn gate.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			<-start
			for n := 0; n < iterations; n++ {
				idx := (n + reader) % objects
				_, err := reg.Read(items[idx].ptr, ctxA, 0, 8)
				switch {
				case err == nil:
					allowed.Add(1)
				case errors.Is(err, ErrGenerationRetired) || errors.Is(err, ErrGenerationStale):
					denied.Add(1)
				default:
					t.Errorf("unexpected gate error: %v", err)
					return
				}
			}
		}(r)
	}

	// A reallocator frees each object and reuses the same address, forcing
	// generation transitions under contention.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for _, it := range items {
			if err := reg.Free(it.obj); err != nil {
				t.Errorf("concurrent Free: %v", err)
				return
			}
			if _, err := reg.RegisterData(it.backing); err != nil {
				t.Errorf("concurrent re-register: %v", err)
				return
			}
		}
	}()

	close(start)
	wg.Wait()

	if allowed.Load()+denied.Load() != int64(readers*iterations) {
		t.Fatalf("accounting mismatch: allowed=%d denied=%d", allowed.Load(), denied.Load())
	}
	if denied.Load() == 0 {
		t.Fatal("expected at least one generation denial under concurrent free")
	}

	// After the dust settles every generation-1 tag must be stale.
	for _, it := range items {
		if err := reg.Check(it.ptr, ctxA); !errors.Is(err, ErrGenerationStale) {
			t.Fatalf("post-race stale tag: expected ErrGenerationStale, got %v", err)
		}
	}
}

func TestConcurrentDereferenceVsFreeIsAtomic(t *testing.T) {
	reg := newTestRegistry(t)

	const rounds = 100
	for round := 0; round < rounds; round++ {
		backing := make([]byte, 16)
		obj, err := reg.RegisterData(backing)
		if err != nil {
			t.Fatalf("RegisterData: %v", err)
		}
		ptr, err := reg.Tag(obj, KindReadWrite, ctxA)
		if err != nil {
			t.Fatalf("Tag: %v", err)
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = reg.Write(ptr, ctxA, 0, []byte("atomic"))
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = reg.Free(obj)
		}()
		close(start)
		wg.Wait()

		// Exactly one terminal state: either the write landed before
		// retirement, or retirement won and the write was denied.
		err = reg.Check(ptr, ctxA)
		switch {
		case errors.Is(err, ErrGenerationRetired):
		case err == nil:
		default:
			t.Fatalf("round %d: unexpected state %v", round, err)
		}
	}
}

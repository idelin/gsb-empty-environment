package netsec

import (
	"context"
	"fmt"
	"sync"
)

// genState tracks the retirement state and the two reference classes that keep
// an old key alive: blocks still sealed under the generation and readers that
// have pinned the generation while decrypting.
type genState struct {
	retired   bool
	blocks    int
	readers   int
	destroyed bool
}

// alive reports whether key material for the generation must remain resident.
// A generation is alive until it has been retired, every block sealed under it
// has been migrated away, and every pinned reader has released it.
func (g genState) alive() bool {
	return !g.retired || g.blocks > 0 || g.readers > 0
}

// KeyRing coordinates the lifecycle of volume key generations. A generation is
// never destroyed while either old-generation blocks or active readers remain.
//
// Key material itself is owned by the Encryptor; the ring only tracks
// references and signals when a generation is safe to zeroize.
type KeyRing struct {
	mu      sync.Mutex
	cond    *sync.Cond
	current KeyGen
	gen     map[KeyGen]*genState

	// OnDestroy, when set, is invoked once per generation at the moment its
	// reference counts reach zero. It is the hook for zeroizing key material,
	// for example AESGCMEncryptor.RemoveKey.
	OnDestroy func(gen KeyGen)
}

// NewKeyRing builds a ring whose current generation is gen 0.
func NewKeyRing() *KeyRing {
	r := &KeyRing{
		current: 0,
		gen:     map[KeyGen]*genState{},
	}
	r.cond = sync.NewCond(&r.mu)
	r.gen[0] = &genState{}
	return r
}

// Current returns the newest provisioned generation. New writes are sealed
// under this generation.
func (r *KeyRing) Current() KeyGen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// Rotate introduces a new current generation and retires every older one.
// Already destroyed generations are skipped. Old keys stay resident until
// their block and reader references drain.
func (r *KeyRing) Rotate(newGen KeyGen) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if newGen <= r.current {
		return fmt.Errorf("netsec: generation %d is not newer than current %d", newGen, r.current)
	}
	if _, exists := r.gen[newGen]; exists {
		return fmt.Errorf("netsec: generation %d already exists", newGen)
	}
	for gen, st := range r.gen {
		if gen < newGen && !st.destroyed {
			st.retired = true
		}
	}
	r.gen[newGen] = &genState{}
	r.current = newGen
	r.cond.Broadcast()
	return nil
}

// IsRetired reports whether gen has been superseded by a rotation.
func (r *KeyRing) IsRetired(gen KeyGen) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.gen[gen]
	return ok && st.retired
}

// IsDestroyed reports whether gen's key material has already been zeroized.
func (r *KeyRing) IsDestroyed(gen KeyGen) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.gen[gen]
	return ok && st.destroyed
}

// PinReader marks an active reader that needs gen to remain available. It
// fails once the generation has already been destroyed. Every successful pin
// must be paired with ReleaseReader.
func (r *KeyRing) PinReader(gen KeyGen) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.gen[gen]
	if !ok {
		return fmt.Errorf("%w: gen %d", ErrUnknownKey, gen)
	}
	if st.destroyed {
		return fmt.Errorf("%w: gen %d", ErrKeyDestroyed, gen)
	}
	st.readers++
	return nil
}

// ReleaseReader releases a pin acquired with PinReader.
func (r *KeyRing) ReleaseReader(gen KeyGen) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.gen[gen]
	if !ok || st.readers == 0 {
		return
	}
	st.readers--
	r.tryDestroyLocked(gen)
}

// AddBlock records that one additional block is sealed under gen.
func (r *KeyRing) AddBlock(gen KeyGen) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.gen[gen]
	if !ok {
		st = &genState{retired: gen != r.current}
		r.gen[gen] = st
	}
	st.blocks++
}

// MoveBlock transfers a block reference from oldGen to newGen after a
// successful re-encryption. The old generation may become destroyable.
func (r *KeyRing) MoveBlock(oldGen, newGen KeyGen) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if oldGen == newGen {
		return
	}
	if st, ok := r.gen[oldGen]; ok && st.blocks > 0 {
		st.blocks--
		r.tryDestroyLocked(oldGen)
	}
	dst, ok := r.gen[newGen]
	if !ok {
		dst = &genState{retired: newGen != r.current}
		r.gen[newGen] = dst
	}
	dst.blocks++
}

// RemoveBlock drops the block reference for gen, e.g. when a block is deleted.
func (r *KeyRing) RemoveBlock(gen KeyGen) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.gen[gen]; ok && st.blocks > 0 {
		st.blocks--
		r.tryDestroyLocked(gen)
	}
}

// BlockCount returns the number of blocks still sealed under gen.
func (r *KeyRing) BlockCount(gen KeyGen) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.gen[gen]; ok {
		return st.blocks
	}
	return 0
}

// ReaderCount returns the number of active readers pinned to gen.
func (r *KeyRing) ReaderCount(gen KeyGen) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.gen[gen]; ok {
		return st.readers
	}
	return 0
}

// tryDestroyLocked zeroizes a retired generation whose references have fully
// drained. Callers must hold r.mu.
func (r *KeyRing) tryDestroyLocked(gen KeyGen) {
	st, ok := r.gen[gen]
	if !ok || st.destroyed || st.alive() {
		return
	}
	st.destroyed = true
	st.blocks = 0
	st.readers = 0
	if r.OnDestroy != nil {
		r.OnDestroy(gen)
	}
	r.cond.Broadcast()
}

// WaitDestroyed blocks until gen has been destroyed or ctx is cancelled.
func (r *KeyRing) WaitDestroyed(ctx context.Context, gen KeyGen) error {
	done := make(chan struct{})
	go func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for {
			if st, ok := r.gen[gen]; ok && st.destroyed {
				close(done)
				return
			}
			if ctx.Err() != nil {
				close(done)
				return
			}
			r.cond.Wait()
		}
	}()
	select {
	case <-done:
		r.mu.Lock()
		destroyed := false
		if st, ok := r.gen[gen]; ok {
			destroyed = st.destroyed
		}
		r.mu.Unlock()
		if destroyed {
			return nil
		}
		return ctx.Err()
	case <-ctx.Done():
		// Wake the waiter goroutine so it can observe the cancellation on
		// its next loop and exit.
		r.cond.Broadcast()
		<-done
		return ctx.Err()
	}
}

// Reconcile rebuilds block reference counts from the current contents of
// store after a process restart. Reader pins are inherently transient and
// always start at zero. Retired generations with no remaining blocks are
// destroyed immediately; generations absent from the ring (key material no
// longer provisioned) are recorded as already destroyed.
func (r *KeyRing) Reconcile(ctx context.Context, store Store) error {
	records, err := store.List(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := make(map[KeyGen]int)
	for _, rec := range records {
		counts[rec.Version.Gen]++
	}
	for gen := range r.gen {
		r.gen[gen].blocks = counts[gen]
		r.gen[gen].readers = 0
		// Reader pins cannot survive a restart, and any generation below the
		// current one was rotated away before the crash.
		if gen < r.current {
			r.gen[gen].retired = true
		}
	}
	for gen, n := range counts {
		st, ok := r.gen[gen]
		if !ok {
			r.gen[gen] = &genState{retired: true, blocks: n, readers: 0, destroyed: false}
			continue
		}
		st.blocks = n
	}
	for gen := range r.gen {
		r.tryDestroyLocked(gen)
	}
	return nil
}

package migsim

import (
	"crypto/sha256"
	"errors"
	"sync"
)

var (
	// ErrMigrationBusy is returned when a block is already being migrated.
	ErrMigrationBusy = errors.New("migsim: migration already in progress")
	// ErrDigestMismatch is returned when the staged copy fails digest
	// verification; the generational switch is not performed.
	ErrDigestMismatch = errors.New("migsim: staged copy digest mismatch")
	// ErrMigrationCancelled is the Wait result of a cancelled migration.
	ErrMigrationCancelled = errors.New("migsim: migration cancelled")
	// ErrAlreadySwitched is returned when cancelling after the switch.
	ErrAlreadySwitched = errors.New("migsim: migration already switched")
	// ErrMigrationFinished is returned when cancelling a finished migration.
	ErrMigrationFinished = errors.New("migsim: migration already finished")
)

// Migrator copies shared blocks between volumes using per-block
// generational address switching.
type Migrator struct {
	// BeforeSwitch, if set, is called after the staged copy is verified and
	// before the atomic generational switch. It runs without holding the
	// block lock, so references, reads and cancellations keep progressing.
	// Test hook used as a concurrency barrier.
	BeforeSwitch func()
}

type migState int

const (
	migCopying migState = iota
	migSwitched
	migCancelled
)

// Migration tracks one in-flight block migration.
type Migration struct {
	mu     sync.Mutex
	state  migState
	block  *Block
	dst    *Volume
	newGen *Generation
	done   chan struct{}
	err    error
}

// Start freezes the block content, stages a copy on dst and verifies its
// digest. A digest mismatch aborts the migration without any switch: the
// staged replica is dropped and the block is unfrozen. On success the
// generational switch is performed asynchronously; use Cancel to abort
// before the switch and Wait to observe the outcome.
func (m *Migrator) Start(b *Block, dst *Volume) (*Migration, error) {
	b.mu.Lock()
	if b.frozen {
		b.mu.Unlock()
		return nil, ErrMigrationBusy
	}
	b.frozen = true
	content := append([]byte(nil), b.content...)
	digest := b.digest
	b.mu.Unlock()

	addr := dst.Put(content)
	copied, ok := dst.Get(addr)
	if !ok || sha256.Sum256(copied) != digest {
		dst.Delete(addr)
		b.mu.Lock()
		b.frozen = false
		b.mu.Unlock()
		return nil, ErrDigestMismatch
	}

	b.mu.Lock()
	b.nextGen++
	gen := &Generation{
		ID:     b.nextGen,
		Vol:    dst,
		Addr:   addr,
		Digest: digest,
		state:  genCopying,
	}
	b.gens = append(b.gens, gen)
	b.mu.Unlock()

	mg := &Migration{
		state:  migCopying,
		block:  b,
		dst:    dst,
		newGen: gen,
		done:   make(chan struct{}),
	}
	go mg.run(m.BeforeSwitch)
	return mg, nil
}

// Cancel aborts the migration if the generational switch has not happened
// yet. The staged replica is discarded and the block keeps serving the old
// generation. Cancelling after the switch fails with ErrAlreadySwitched.
func (mg *Migration) Cancel() error {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	switch mg.state {
	case migCopying:
		mg.state = migCancelled
		return nil
	case migSwitched:
		return ErrAlreadySwitched
	default:
		return ErrMigrationFinished
	}
}

// Wait blocks until the migration settles and reports the outcome: nil on
// switch, ErrMigrationCancelled on cancel.
func (mg *Migration) Wait() error {
	<-mg.done
	mg.mu.Lock()
	defer mg.mu.Unlock()
	return mg.err
}

// run performs the switch (or observes a cancellation) after the optional
// barrier hook releases.
func (mg *Migration) run(beforeSwitch func()) {
	if beforeSwitch != nil {
		beforeSwitch()
	}

	mg.mu.Lock()
	if mg.state == migCancelled {
		mg.mu.Unlock()
		mg.dst.Delete(mg.newGen.Addr)
		b := mg.block
		b.mu.Lock()
		mg.newGen.state = genAborted
		b.frozen = false
		b.mu.Unlock()
		mg.finish(ErrMigrationCancelled)
		return
	}
	mg.state = migSwitched
	mg.mu.Unlock()

	b := mg.block
	b.mu.Lock()
	old := b.cur
	old.state = genRetired
	mg.newGen.state = genActive
	b.cur = mg.newGen
	b.frozen = false
	b.reclaimLocked()
	b.mu.Unlock()
	mg.finish(nil)
}

func (mg *Migration) finish(err error) {
	mg.mu.Lock()
	mg.err = err
	mg.mu.Unlock()
	close(mg.done)
}

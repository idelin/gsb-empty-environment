package migsim

import (
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	// ErrFrozen is returned when content is written while a migration copy
	// is in progress: content must stay immutable during the copy.
	ErrFrozen = errors.New("migsim: block content frozen during migration")
	// ErrPinned is returned when content is written while read handles pin
	// the active generation.
	ErrPinned = errors.New("migsim: active generation has open read handles")
	// ErrNoActiveGeneration is returned when a block has no active replica.
	ErrNoActiveGeneration = errors.New("migsim: block has no active generation")
	// ErrClosed is returned when reading a closed handle.
	ErrClosed = errors.New("migsim: read handle is closed")
	// ErrReplicaGone is returned when the pinned replica no longer exists.
	ErrReplicaGone = errors.New("migsim: pinned replica is gone")
)

// genState is the lifecycle state of a generation.
type genState int

const (
	genCopying   genState = iota // staged by an in-flight migration, not yet visible
	genActive                    // current generation: new references resolve here
	genRetired                   // superseded; kept alive while handles are pinned
	genReclaimed                 // retired and drained; physical replica deleted
	genAborted                   // staged by a cancelled/failed migration; cleaned up
)

// Generation is one physical incarnation of a block: a replica at a fixed
// (volume, address) with a verified digest. Read handles pin a generation;
// reclamation of a retired generation waits until its handles drain.
type Generation struct {
	ID     uint64
	Vol    *Volume
	Addr   string
	Digest [sha256.Size]byte

	state   genState
	handles int
}

// Block is a logical shared block. Its content is immutable while frozen by
// a migration. New references and new reads always bind to the current
// generation; the switch between generations is atomic per block.
type Block struct {
	mu      sync.Mutex
	id      string
	content []byte
	digest  [sha256.Size]byte
	frozen  bool
	cur     *Generation
	gens    []*Generation
	nextGen uint64
}

// NewBlock creates a block whose first generation lives on home.
func NewBlock(id string, content []byte, home *Volume) *Block {
	b := &Block{id: id}
	b.content = append([]byte(nil), content...)
	b.digest = sha256.Sum256(b.content)
	gen := &Generation{
		ID:     1,
		Vol:    home,
		Addr:   home.Put(b.content),
		Digest: b.digest,
		state:  genActive,
	}
	b.nextGen = 1
	b.cur = gen
	b.gens = append(b.gens, gen)
	return b
}

// ID returns the block identifier.
func (b *Block) ID() string { return b.id }

// Write replaces the block content. It fails with ErrFrozen while a
// migration copy is in progress, and with ErrPinned while read handles pin
// the active generation.
func (b *Block) Write(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.frozen {
		return ErrFrozen
	}
	if b.cur.handles != 0 {
		return ErrPinned
	}
	b.content = append([]byte(nil), data...)
	b.digest = sha256.Sum256(b.content)
	b.cur.Vol.Delete(b.cur.Addr)
	b.cur.Addr = b.cur.Vol.Put(b.content)
	b.cur.Digest = b.digest
	return nil
}

// Frozen reports whether a migration copy is in progress.
func (b *Block) Frozen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.frozen
}

// OpenRead pins the current generation and returns a handle bound to its
// physical replica. The handle keeps reading that replica even after a
// generational switch; the old replica is reclaimed only after the last
// pinned handle closes.
func (b *Block) OpenRead() (*ReadHandle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cur == nil || b.cur.state != genActive {
		return nil, ErrNoActiveGeneration
	}
	b.cur.handles++
	return &ReadHandle{block: b, gen: b.cur}, nil
}

// reclaimLocked deletes the physical replica of every retired generation
// whose handles have fully drained. Called on switch and on handle close.
func (b *Block) reclaimLocked() {
	for _, g := range b.gens {
		if g.state == genRetired && g.handles == 0 {
			g.Vol.Delete(g.Addr)
			g.state = genReclaimed
		}
	}
}

// ReadHandle pins one generation's replica for reading.
type ReadHandle struct {
	block  *Block
	gen    *Generation
	once   sync.Once
	closed atomic.Bool
}

// Read returns the pinned replica's content.
func (h *ReadHandle) Read() ([]byte, error) {
	if h.closed.Load() {
		return nil, ErrClosed
	}
	data, ok := h.gen.Vol.Get(h.gen.Addr)
	if !ok {
		return nil, ErrReplicaGone
	}
	return data, nil
}

// Close releases the pin. When the last handle on a retired generation
// closes, its physical replica is reclaimed.
func (h *ReadHandle) Close() {
	h.once.Do(func() {
		h.closed.Store(true)
		b := h.block
		b.mu.Lock()
		h.gen.handles--
		b.reclaimLocked()
		b.mu.Unlock()
	})
}

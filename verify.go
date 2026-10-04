package migsim

import (
	"crypto/sha256"
	"fmt"
)

// Verify cross-checks the independent ledger against the physical state of
// the given blocks. It confirms that every live ledger reference resolves
// to an active, physically present replica, and that every generation is in
// a consistent state with its volume.
func Verify(l *Ledger, blocks ...*Block) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	byID := make(map[string]*Block, len(blocks))
	for _, b := range blocks {
		byID[b.id] = b
	}
	for obj, bs := range l.refs {
		for blkID, n := range bs {
			if n <= 0 {
				continue
			}
			b := byID[blkID]
			if b == nil {
				return fmt.Errorf("ledger: %s references unknown block %s", obj, blkID)
			}
			b.mu.Lock()
			ok := b.cur != nil && b.cur.state == genActive && b.cur.Vol.Has(b.cur.Addr)
			b.mu.Unlock()
			if !ok {
				return fmt.Errorf("ledger: dangling reference %s -> %s", obj, blkID)
			}
		}
	}
	for _, b := range blocks {
		if err := b.checkInvariants(); err != nil {
			return err
		}
	}
	return nil
}

// checkInvariants validates the generation table of one block against the
// physical volumes.
func (b *Block) checkInvariants() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	active := 0
	for _, g := range b.gens {
		if g.handles < 0 {
			return fmt.Errorf("block %s gen %d: negative handle count", b.id, g.ID)
		}
		switch g.state {
		case genActive:
			active++
			data, ok := g.Vol.Get(g.Addr)
			if !ok {
				return fmt.Errorf("block %s: active replica %s missing", b.id, g.Addr)
			}
			if sha256.Sum256(data) != b.digest {
				return fmt.Errorf("block %s: active replica %s digest drift", b.id, g.Addr)
			}
		case genCopying:
			if !b.frozen {
				return fmt.Errorf("block %s: copying generation %d but block not frozen", b.id, g.ID)
			}
		case genRetired:
			if g.handles == 0 {
				return fmt.Errorf("block %s: retired generation %d drained but not reclaimed", b.id, g.ID)
			}
			if !g.Vol.Has(g.Addr) {
				return fmt.Errorf("block %s: pinned replica %s missing", b.id, g.Addr)
			}
		case genReclaimed:
			if g.Vol.Has(g.Addr) {
				return fmt.Errorf("block %s: reclaimed replica %s still present", b.id, g.Addr)
			}
		case genAborted:
			if g.Vol.Has(g.Addr) {
				return fmt.Errorf("block %s: aborted replica %s still present", b.id, g.Addr)
			}
		}
	}
	if active != 1 {
		return fmt.Errorf("block %s: expected exactly 1 active generation, found %d", b.id, active)
	}
	return nil
}

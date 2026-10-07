package pageindex

import (
	"fmt"
	"sync"
)

// PageNo is an explicit, stable page number. Page 0 is reserved as invalid.
type PageNo uint64

// NilPage is the invalid page number.
const NilPage PageNo = 0

// retiredPage is a page that the current root no longer references. It stays
// allocated until every active snapshot that could still reach it is closed.
type retiredPage struct {
	no    PageNo
	epoch uint64 // commit epoch at which the page left the current tree
}

// Pager owns all pages. Pages are immutable once published: writers never
// mutate a page in place, they allocate a fresh page (possibly reusing a
// fully reclaimed number) and retire the old one.
type Pager struct {
	mu      sync.RWMutex
	pages   map[PageNo]*node
	next    PageNo
	free    []PageNo // LIFO; only pages no active snapshot can reach
	retired []retiredPage
}

// NewPager returns an empty pager. Page numbers start at 1.
func NewPager() *Pager {
	return &Pager{pages: make(map[PageNo]*node), next: 1}
}

// alloc publishes n on a recycled page if possible, otherwise on a fresh one.
func (p *Pager) alloc(n *node) PageNo {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.free) > 0 {
		no := p.free[len(p.free)-1]
		p.free = p.free[:len(p.free)-1]
		p.pages[no] = n
		return no
	}
	no := p.next
	p.next++
	p.pages[no] = n
	return no
}

// get returns the immutable node stored on page no. It panics if the page is
// not allocated: that means a dangling reference, i.e. a reclamation bug.
func (p *Pager) get(no PageNo) *node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	n, ok := p.pages[no]
	if !ok {
		panic(fmt.Sprintf("pageindex: dangling reference to page %d", no))
	}
	return n
}

// retire marks no as unreachable from the current root as of epoch.
func (p *Pager) retire(no PageNo, epoch uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retired = append(p.retired, retiredPage{no: no, epoch: epoch})
}

// reclaim moves every retired page that no active snapshot can still reach
// onto the free list and drops its contents. A page retired at epoch e is
// visible to any snapshot pinned at an epoch < e, so it is reclaimable only
// when the oldest active snapshot epoch is >= e. With no active snapshots
// every retired page is reclaimed.
func (p *Pager) reclaim(minActiveEpoch uint64, hasActive bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.retired[:0]
	for _, r := range p.retired {
		if !hasActive || r.epoch <= minActiveEpoch {
			delete(p.pages, r.no)
			p.free = append(p.free, r.no)
		} else {
			kept = append(kept, r)
		}
	}
	p.retired = kept
}

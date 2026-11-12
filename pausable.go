package netsec

import "sync"

// ReadOp identifies the kind of access a PageSource is about to perform.
type ReadOp int

const (
	OpCommitGen ReadOp = iota
	OpListPages
	OpReadPage
	OpPageWriteGen
)

func (o ReadOp) String() string {
	switch o {
	case OpCommitGen:
		return "CommitGen"
	case OpListPages:
		return "ListPages"
	case OpReadPage:
		return "ReadPage"
	case OpPageWriteGen:
		return "PageWriteGen"
	}
	return "Unknown"
}

// PausableSource wraps a PageSource and invokes a hook before every access.
// The hook runs outside any internal lock, so it may safely block (to pause
// the scanner) and mutate the underlying image (to inject races). It also
// counts successful ReadPage calls per address so tests can prove that only
// affected pages were re-read.
type PausableSource struct {
	src PageSource

	mu   sync.RWMutex
	hook func(op ReadOp, addr uint64)

	cntMu sync.Mutex
	reads map[uint64]int
}

// NewPausableSource wraps src.
func NewPausableSource(src PageSource) *PausableSource {
	return &PausableSource{src: src, reads: make(map[uint64]int)}
}

// SetHook installs the pre-access hook; nil clears it.
func (p *PausableSource) SetHook(h func(op ReadOp, addr uint64)) {
	p.mu.Lock()
	p.hook = h
	p.mu.Unlock()
}

func (p *PausableSource) fire(op ReadOp, addr uint64) {
	p.mu.RLock()
	h := p.hook
	p.mu.RUnlock()
	if h != nil {
		h(op, addr)
	}
}

// ReadCount reports how many times page content at addr was read successfully.
func (p *PausableSource) ReadCount(addr uint64) int {
	p.cntMu.Lock()
	defer p.cntMu.Unlock()
	return p.reads[addr]
}

func (p *PausableSource) CommitGen() uint64 {
	p.fire(OpCommitGen, 0)
	return p.src.CommitGen()
}

func (p *PausableSource) ListPages() ([]uint64, error) {
	p.fire(OpListPages, 0)
	return p.src.ListPages()
}

func (p *PausableSource) ReadPage(addr uint64) ([]byte, uint64, error) {
	p.fire(OpReadPage, addr)
	data, gen, err := p.src.ReadPage(addr)
	if err == nil {
		p.cntMu.Lock()
		p.reads[addr]++
		p.cntMu.Unlock()
	}
	return data, gen, err
}

func (p *PausableSource) PageWriteGen(addr uint64) (uint64, error) {
	p.fire(OpPageWriteGen, addr)
	return p.src.PageWriteGen(addr)
}

// Gate blocks scanner progress while paused. It is designed for a
// single-threaded scanner: at most one access is held at a time.
type Gate struct {
	mu      sync.Mutex
	paused  bool
	entered chan struct{}
	resume  chan struct{}
}

func NewGate() *Gate {
	return &Gate{entered: make(chan struct{}, 1)}
}

// Pause arms the gate; the next Hold blocks until Resume.
func (g *Gate) Pause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.entered:
	default:
	}
	g.paused = true
	g.resume = make(chan struct{})
}

// Hold blocks while the gate is paused. Typically called from a
// PausableSource hook.
func (g *Gate) Hold() {
	g.mu.Lock()
	if !g.paused {
		g.mu.Unlock()
		return
	}
	resume := g.resume
	g.mu.Unlock()
	g.entered <- struct{}{}
	<-resume
}

// WaitEntered blocks until an access is held by the gate.
func (g *Gate) WaitEntered() {
	<-g.entered
}

// Resume releases the held access and disarms the gate.
func (g *Gate) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		return
	}
	g.paused = false
	close(g.resume)
}

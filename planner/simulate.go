package planner

import "fmt"

// diskState tracks the set of live chunks. A chunk occupies disk while at
// least one stage references it; shared chunks are counted exactly once.
type diskState struct {
	sizes map[string]int64
	refs  map[string]int
	used  int64
	peak  int64
}

func newDiskState() diskState {
	return diskState{sizes: map[string]int64{}, refs: map[string]int{}}
}

func (d diskState) clone() diskState {
	sizes := make(map[string]int64, len(d.sizes))
	for k, v := range d.sizes {
		sizes[k] = v
	}
	refs := make(map[string]int, len(d.refs))
	for k, v := range d.refs {
		refs[k] = v
	}
	return diskState{sizes: sizes, refs: refs, used: d.used, peak: d.peak}
}

func (d *diskState) alloc(chunks []Chunk) {
	for _, c := range chunks {
		if d.refs[c.Name] == 0 {
			d.sizes[c.Name] = c.Size
			d.used += c.Size
		}
		d.refs[c.Name]++
	}
	if d.used > d.peak {
		d.peak = d.used
	}
}

func (d *diskState) free(chunks []Chunk) {
	for _, c := range chunks {
		d.refs[c.Name]--
		if d.refs[c.Name] == 0 {
			d.used -= d.sizes[c.Name]
			delete(d.refs, c.Name)
			delete(d.sizes, c.Name)
		}
	}
}

// state is a point-in-time execution state: which blocks are scheduled and
// which chunks are live.
type state struct {
	disk diskState
	// scheduled[i] reports whether block i has fully published.
	scheduled       []bool
	nScheduled      int
	unpubDependents []int
	log             *[]TracePoint
}

func (p *Problem) newState() *state {
	st := &state{
		disk:            newDiskState(),
		scheduled:       make([]bool, len(p.blocks)),
		unpubDependents: make([]int, len(p.blocks)),
	}
	for i := range p.blocks {
		st.unpubDependents[i] = len(p.dependents[i])
	}
	return st
}

func (st *state) clone() *state {
	return &state{
		disk:            st.disk.clone(),
		scheduled:       append([]bool(nil), st.scheduled...),
		nScheduled:      st.nScheduled,
		unpubDependents: append([]int(nil), st.unpubDependents...),
		log:             st.log,
	}
}

func (st *state) tracef(block, event string) {
	if st.log != nil {
		*st.log = append(*st.log, TracePoint{
			Block: block,
			Event: event,
			Used:  st.disk.used,
			Peak:  st.disk.peak,
		})
	}
}

// apply executes all three stages of block b against st, applying releases
// at their configured release points.
func (p *Problem) apply(st *state, b int) {
	blk := &p.blocks[b]
	st.disk.alloc(blk.Download.Chunks)
	st.tracef(blk.ID, "download")
	st.disk.alloc(blk.Temp.Chunks)
	st.tracef(blk.ID, "decompress")
	p.releaseAt(st, b, ReleaseOnDecompress)
	st.disk.alloc(blk.Final.Chunks)
	st.tracef(blk.ID, "publish")
	p.releaseAt(st, b, ReleaseOnPublish)
	st.scheduled[b] = true
	st.nScheduled++
	for _, u := range p.deps[b] {
		st.unpubDependents[u]--
		if st.unpubDependents[u] == 0 && st.scheduled[u] {
			p.releaseAt(st, u, ReleaseAfterDependents)
		}
	}
	if st.unpubDependents[b] == 0 {
		p.releaseAt(st, b, ReleaseAfterDependents)
	}
}

func (p *Problem) releaseAt(st *state, b int, rp ReleasePoint) {
	blk := &p.blocks[b]
	for _, s := range []Stage{blk.Download, blk.Temp, blk.Final} {
		if s.Release == rp {
			st.disk.free(s.Chunks)
			st.tracef(blk.ID, "release("+string(rp)+")")
		}
	}
}

// Simulation is the outcome of replaying an order.
type Simulation struct {
	// Peak is the maximum disk used at any point. It can exceed Final:
	// the final size must never be mistaken for the peak.
	Peak int64
	// Final is the disk still used when the plan completes.
	Final int64
}

// TracePoint records disk usage right after one event.
type TracePoint struct {
	Block string `json:"block"`
	Event string `json:"event"`
	Used  int64  `json:"used"`
	Peak  int64  `json:"peak"`
}

// Simulate replays a full execution order and reports peak and final usage.
// The order must be a permutation of all blocks respecting dependencies.
func (p *Problem) Simulate(order []int) (Simulation, error) {
	_, sim, err := p.run(order, false)
	return sim, err
}

// Trace replays an order and also returns the per-event disk usage trace.
func (p *Problem) Trace(order []int) ([]TracePoint, Simulation, error) {
	return p.run(order, true)
}

func (p *Problem) run(order []int, withLog bool) ([]TracePoint, Simulation, error) {
	n := len(p.blocks)
	if len(order) != n {
		return nil, Simulation{}, fmt.Errorf("order has %d blocks, want %d", len(order), n)
	}
	st := p.newState()
	var log []TracePoint
	if withLog {
		st.log = &log
	}
	for _, b := range order {
		if b < 0 || b >= n {
			return nil, Simulation{}, fmt.Errorf("order contains invalid block index %d", b)
		}
		if st.scheduled[b] {
			return nil, Simulation{}, fmt.Errorf("block %q scheduled twice", p.blocks[b].ID)
		}
		for _, d := range p.deps[b] {
			if !st.scheduled[d] {
				return nil, Simulation{}, fmt.Errorf("block %q scheduled before dependency %q",
					p.blocks[b].ID, p.blocks[d].ID)
			}
		}
		p.apply(st, b)
	}
	return log, Simulation{Peak: st.disk.peak, Final: st.disk.used}, nil
}

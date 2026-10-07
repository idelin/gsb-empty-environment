// Package tracer traces solute masses and provenance (source-batch shares)
// across lab containers as integer-volume transfers, water additions and
// mixing are recorded. All arithmetic is exact rational arithmetic.
package tracer

import (
	"fmt"
	"math/big"
)

// TransferOp describes one integer-volume transfer from Src to Dst.
type TransferOp struct {
	Src    string
	Dst    string
	Volume int64
}

// containerState is the mutable internal state of one container.
// batchVol maps a source-batch label to the volume-equivalent of that
// batch present in the (well-mixed) contents; its sum always equals volume.
type containerState struct {
	volume   *big.Rat
	masses   map[string]*big.Rat // solute -> mass
	batchVol map[string]*big.Rat // batch label -> volume-equivalent
}

// System is the ledger of containers, batches and recorded operations.
type System struct {
	containers map[string]*containerState
	batches    map[string]*big.Rat // batch label -> introduced volume
	introduced map[string]*big.Rat // solute -> total mass ever introduced
	waterSeq   int
	log        []string
}

// NewSystem returns an empty tracing system.
func NewSystem() *System {
	return &System{
		containers: map[string]*containerState{},
		batches:    map[string]*big.Rat{},
		introduced: map[string]*big.Rat{},
	}
}

func newContainer() *containerState {
	return &containerState{
		volume:   new(big.Rat),
		masses:   map[string]*big.Rat{},
		batchVol: map[string]*big.Rat{},
	}
}

func cloneState(c *containerState) *containerState {
	out := &containerState{
		volume:   new(big.Rat).Set(c.volume),
		masses:   map[string]*big.Rat{},
		batchVol: map[string]*big.Rat{},
	}
	for k, v := range c.masses {
		out.masses[k] = new(big.Rat).Set(v)
	}
	for k, v := range c.batchVol {
		out.batchVol[k] = new(big.Rat).Set(v)
	}
	return out
}

func addTo(m map[string]*big.Rat, key string, v *big.Rat) {
	cur, ok := m[key]
	if !ok {
		m[key] = new(big.Rat).Set(v)
		return
	}
	cur.Add(cur, v)
}

func subFrom(m map[string]*big.Rat, key string, v *big.Rat) {
	cur := m[key]
	cur.Sub(cur, v)
	if cur.Sign() == 0 {
		delete(m, key)
	}
}

// AddContainer registers a container with an integer initial volume and
// rational initial solute masses (e.g. "1/3", "2.5", "7"). A non-empty
// initial content forms the source batch "<id>#init".
func (s *System) AddContainer(id string, volume int64, masses map[string]string) error {
	if id == "" {
		return fmt.Errorf("container id must not be empty")
	}
	if _, ok := s.containers[id]; ok {
		return fmt.Errorf("container %q already exists", id)
	}
	if volume < 0 {
		return fmt.Errorf("container %q: negative volume %d", id, volume)
	}
	c := newContainer()
	c.volume.SetInt64(volume)
	for name, str := range masses {
		m, ok := new(big.Rat).SetString(str)
		if !ok {
			return fmt.Errorf("container %q: invalid rational mass %q for solute %q", id, str, name)
		}
		if m.Sign() < 0 {
			return fmt.Errorf("container %q: negative mass for solute %q", id, name)
		}
		if volume == 0 && m.Sign() != 0 {
			return fmt.Errorf("container %q: positive mass of %q with zero volume", id, name)
		}
		if m.Sign() != 0 {
			c.masses[name] = m
			prev, ok := s.introduced[name]
			if !ok {
				prev = new(big.Rat)
			}
			s.introduced[name] = prev.Add(prev, m)
		}
	}
	s.containers[id] = c
	if volume > 0 {
		label := id + "#init"
		c.batchVol[label] = new(big.Rat).SetInt64(volume)
		s.batches[label] = new(big.Rat).SetInt64(volume)
	}
	s.log = append(s.log, fmt.Sprintf("add-container %s volume=%d", id, volume))
	return nil
}

// AddWater adds an integer volume of water (no solute) to a container,
// creating a new source batch "<id>#water<n>". Returns the batch label.
func (s *System) AddWater(id string, volume int64) (string, error) {
	c, ok := s.containers[id]
	if !ok {
		return "", fmt.Errorf("container %q does not exist", id)
	}
	if volume < 0 {
		return "", fmt.Errorf("container %q: negative water volume %d", id, volume)
	}
	if volume == 0 {
		return "", nil
	}
	s.waterSeq++
	label := fmt.Sprintf("%s#water%d", id, s.waterSeq)
	c.volume.Add(c.volume, new(big.Rat).SetInt64(volume))
	addTo(c.batchVol, label, new(big.Rat).SetInt64(volume))
	s.batches[label] = new(big.Rat).SetInt64(volume)
	s.log = append(s.log, fmt.Sprintf("add-water %s volume=%d batch=%s", id, volume, label))
	return label, nil
}

// Mix records homogenization of a container. State is always treated as
// well-mixed, so this only appends to the audit log.
func (s *System) Mix(id string) error {
	if _, ok := s.containers[id]; !ok {
		return fmt.Errorf("container %q does not exist", id)
	}
	s.log = append(s.log, "mix "+id)
	return nil
}

// Transfer moves an integer volume from src to dst, taking each solute and
// each batch share proportionally to the source's current concentration.
// If the volume exceeds the source's current volume the transfer is
// rejected and no state changes.
func (s *System) Transfer(src, dst string, volume int64) error {
	return s.TransferBatch([]TransferOp{{Src: src, Dst: dst, Volume: volume}})
}

// TransferBatch applies a batch of transfers. Every take is computed
// against the same pre-batch state; if the summed draws from any source
// exceed its current volume, the whole batch is rejected atomically.
func (s *System) TransferBatch(ops []TransferOp) error {
	if len(ops) == 0 {
		return nil
	}
	totals := map[string]*big.Rat{}
	for i, op := range ops {
		if op.Volume < 0 {
			return fmt.Errorf("op %d: negative volume %d", i, op.Volume)
		}
		if _, ok := s.containers[op.Src]; !ok {
			return fmt.Errorf("op %d: source %q does not exist", i, op.Src)
		}
		if _, ok := s.containers[op.Dst]; !ok {
			return fmt.Errorf("op %d: destination %q does not exist", i, op.Dst)
		}
		t := totals[op.Src]
		if t == nil {
			t = new(big.Rat)
			totals[op.Src] = t
		}
		t.Add(t, new(big.Rat).SetInt64(op.Volume))
	}
	for src, t := range totals {
		if t.Cmp(s.containers[src].volume) > 0 {
			return fmt.Errorf("batch rejected: total draw %s from %q exceeds available volume %s",
				t, src, s.containers[src].volume)
		}
	}

	// Snapshot the pre-batch state of every involved source.
	old := map[string]*containerState{}
	for _, op := range ops {
		if op.Volume > 0 {
			if _, ok := old[op.Src]; !ok {
				old[op.Src] = cloneState(s.containers[op.Src])
			}
		}
	}

	type take struct {
		src, dst string
		volume   *big.Rat
		masses   map[string]*big.Rat
		batchVol map[string]*big.Rat
	}
	var takes []take
	for _, op := range ops {
		if op.Volume == 0 {
			continue
		}
		src := old[op.Src]
		frac := new(big.Rat).SetInt64(op.Volume)
		frac.Quo(frac, src.volume)
		tk := take{
			src:      op.Src,
			dst:      op.Dst,
			volume:   new(big.Rat).SetInt64(op.Volume),
			masses:   map[string]*big.Rat{},
			batchVol: map[string]*big.Rat{},
		}
		for name, m := range src.masses {
			tk.masses[name] = new(big.Rat).Mul(frac, m)
		}
		for b, v := range src.batchVol {
			tk.batchVol[b] = new(big.Rat).Mul(frac, v)
		}
		takes = append(takes, tk)
	}
	for _, tk := range takes {
		src := s.containers[tk.src]
		dst := s.containers[tk.dst]
		src.volume.Sub(src.volume, tk.volume)
		dst.volume.Add(dst.volume, tk.volume)
		for name, m := range tk.masses {
			subFrom(src.masses, name, m)
			addTo(dst.masses, name, m)
		}
		for b, v := range tk.batchVol {
			subFrom(src.batchVol, b, v)
			addTo(dst.batchVol, b, v)
		}
	}
	for _, op := range ops {
		s.log = append(s.log, fmt.Sprintf("transfer %s -> %s volume=%d", op.Src, op.Dst, op.Volume))
	}
	return nil
}

// Log returns a copy of the audit log of recorded operations.
func (s *System) Log() []string {
	out := make([]string, len(s.log))
	copy(out, s.log)
	return out
}

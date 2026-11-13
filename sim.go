package netsec

import (
	"fmt"
)

// Config controls the bounded pipeline model.
type Config struct {
	// StoreBufferSize is 1..4. Zero selects MaxStoreBuffer.
	StoreBufferSize int
	// ResolveDelay is 1..4 cycles. Zero selects MaxResolveDelay.
	ResolveDelay int
	// StoreCommitDelay is the number of cycles before a store is committed.
	// Zero selects one cycle.
	StoreCommitDelay int
}

// DefaultConfig returns the model's default timing.
func DefaultConfig() Config {
	return Config{
		StoreBufferSize:  MaxStoreBuffer,
		ResolveDelay:     MaxResolveDelay,
		StoreCommitDelay: 1,
	}
}

func (c Config) normalized() (Config, error) {
	if c.StoreBufferSize == 0 {
		c.StoreBufferSize = MaxStoreBuffer
	}
	if c.ResolveDelay == 0 {
		c.ResolveDelay = MaxResolveDelay
	}
	if c.StoreCommitDelay == 0 {
		c.StoreCommitDelay = 1
	}
	if c.StoreBufferSize < 1 || c.StoreBufferSize > MaxStoreBuffer {
		return Config{}, fmt.Errorf("store buffer size %d out of range 1..%d", c.StoreBufferSize, MaxStoreBuffer)
	}
	if c.ResolveDelay < 1 || c.ResolveDelay > MaxResolveDelay {
		return Config{}, fmt.Errorf("resolve delay %d out of range 1..%d", c.ResolveDelay, MaxResolveDelay)
	}
	if c.StoreCommitDelay < 1 {
		return Config{}, fmt.Errorf("store commit delay %d must be positive", c.StoreCommitDelay)
	}
	return c, nil
}

// StoreSnapshot is a point-in-time view of one store-buffer entry.
type StoreSnapshot struct {
	ID         int
	PC         int
	Addr       Addr
	Value      Value
	Committed  bool
	IssueCycle int
}

// PendingSnapshot is a point-in-time view of an unresolved load.
type PendingSnapshot struct {
	ID           int
	PC           int
	Dst          Reg
	ResolveCycle int
	Mismatch     bool
}

// ProbeRecord is one architectural/speculative observation.
type ProbeRecord struct {
	PC        int
	Cycle     int
	Reg       Reg
	ArchGroup uint8
	SpecGroup uint8
}

// LoadReport describes the forwarding choice and resolution of one load.
type LoadReport struct {
	ID   int
	PC   int
	Addr Addr
	Dst  Reg

	IssueCycle int

	// SelectedStore is the store ID chosen by the partial-address predictor,
	// or -1 when no matching low-six-bit store was buffered.
	SelectedStore          int
	SelectedStorePC        int
	SelectedStoreAddr      Addr
	SelectedStoreValue     Value
	SelectedStoreCommitted bool

	// ArchStore is the full-address store chosen by architectural execution,
	// or -1 when the architectural value came from memory.
	ArchStore   int
	ArchStorePC int
	ArchValue   Value
	SpecValue   Value

	Forwarded  bool
	Mismatch   bool
	Superseded bool

	ResolveCycle      int
	ResolvedAtBarrier bool
	RolledBack        bool
	RegBeforeRollback Value
	RegAfterRollback  Value
}

func (r LoadReport) String() string {
	return fmt.Sprintf(
		"load#%d pc=%d addr=%#03x dst=r%d issue=%d selected=%d arch=%d mismatch=%v resolve=%d barrier=%v superseded=%v rollback[%#02x->%#02x]",
		r.ID, r.PC, r.Addr, r.Dst, r.IssueCycle,
		r.SelectedStore, r.ArchStore, r.Mismatch, r.ResolveCycle,
		r.ResolvedAtBarrier, r.Superseded, r.RegBeforeRollback, r.RegAfterRollback,
	)
}

// CycleState is the cycle-by-cycle reference view. It is intentionally
// recorded even when the final architectural and speculative states agree.
type CycleState struct {
	Cycle          int
	PC             int
	HasInstruction bool
	Kind           Kind

	ArchRegs [RegisterCount]Value
	SpecRegs [RegisterCount]Value

	StoreBuffer []StoreSnapshot
	Pending     []PendingSnapshot

	Probe *ProbeRecord
}

// RunResult contains both traces and the detailed per-load and per-cycle
// reference information.
type RunResult struct {
	Input Input

	ArchProbes []uint8
	SpecProbes []uint8
	Probes     []ProbeRecord

	Loads  []LoadReport
	Cycles []CycleState

	FinalArchRegs   [RegisterCount]Value
	FinalSpecRegs   [RegisterCount]Value
	FinalArchMemory map[Addr]Value
	FinalSpecMemory map[Addr]Value
}

type storeEntry struct {
	id         int
	pc         int
	addr       Addr
	value      Value
	committed  bool
	issueCycle int
}

type loadRecord struct {
	LoadReport
	postXors   []Value
	superseded bool
	resolved   bool
}

type machine struct {
	cfg Config

	archMem  map[Addr]Value
	archRegs [RegisterCount]Value

	specMem  map[Addr]Value
	specRegs [RegisterCount]Value

	storeBuffer []*storeEntry
	pending     []*loadRecord
	allLoads    []*loadRecord
	pendingReg  [RegisterCount]*loadRecord

	nextStoreID int
	nextLoadID  int
}

// Run executes a program with the default configuration.
func Run(program Program, input Input) (*RunResult, error) {
	return RunWithConfig(program, input, DefaultConfig())
}

// RunWithConfig executes a program and records both correct execution and the
// speculative pipeline's behavior.
func RunWithConfig(program Program, input Input, cfg Config) (*RunResult, error) {
	if err := program.Validate(); err != nil {
		return nil, err
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}

	m := &machine{
		cfg:     cfg,
		archMem: cloneMemory(input.Memory),
		specMem: cloneMemory(input.Memory),
	}
	m.archRegs = input.Regs
	m.specRegs = input.Regs

	result := &RunResult{Input: Input{
		Secret:      input.Secret,
		PublicInput: PublicInput{Low6: input.Low6, Memory: cloneMemory(input.Memory), Regs: input.Regs},
	}}

	cycle := 0
	for pc, in := range program {
		m.commitStores(cycle)
		m.resolveDue(cycle)

		addr := Addr(0)
		if in.Kind == KindStore || in.Kind == KindLoad {
			var err error
			addr, err = in.Addr.Resolve(input.Low6)
			if err != nil {
				return nil, fmt.Errorf("instruction %d: %w", pc, err)
			}
		}

		var probe *ProbeRecord
		switch in.Kind {
		case KindStore:
			value := in.Value.Resolve(input.Secret)
			m.archMem[addr] = value
			m.store(cycle, pc, addr, value)
		case KindLoad:
			m.archRegs[in.Reg] = m.archMem[addr]
			m.load(cycle, pc, addr, in.Reg)
		case KindXor:
			m.archRegs[in.Reg] ^= in.Imm
			m.specRegs[in.Reg] ^= in.Imm
			if pending := m.pendingReg[in.Reg]; pending != nil {
				pending.postXors = append(pending.postXors, in.Imm)
			}
		case KindProbe:
			record := ProbeRecord{
				PC:        pc,
				Cycle:     cycle,
				Reg:       in.Reg,
				ArchGroup: uint8(m.archRegs[in.Reg] & 0x0f),
				SpecGroup: uint8(m.specRegs[in.Reg] & 0x0f),
			}
			result.ArchProbes = append(result.ArchProbes, record.ArchGroup)
			result.SpecProbes = append(result.SpecProbes, record.SpecGroup)
			result.Probes = append(result.Probes, record)
			probeCopy := record
			probe = &probeCopy
		case KindBarrier:
			m.resolveAllAtBarrier(cycle)
		}

		result.Cycles = append(result.Cycles, m.snapshot(cycle, pc, in.Kind, probe))
		cycle++
	}

	// Drain outstanding loads. They may resolve after the last instruction.
	for len(m.pending) > 0 {
		m.commitStores(cycle)
		m.resolveDue(cycle)
		result.Cycles = append(result.Cycles, m.snapshot(cycle, -1, KindBarrier, nil))
		cycle++
		if cycle > len(program)+MaxResolveDelay+cfg.StoreCommitDelay+2 {
			return nil, fmt.Errorf("internal error: loads did not drain")
		}
	}

	// Commit all remaining stores so the final memory image is comparable.
	for _, entry := range m.storeBuffer {
		m.specMem[entry.addr] = entry.value
	}
	m.storeBuffer = nil

	result.Loads = make([]LoadReport, 0, len(m.allLoads))
	for _, rec := range m.allLoads {
		result.Loads = append(result.Loads, rec.LoadReport)
	}
	result.FinalArchRegs = m.archRegs
	result.FinalSpecRegs = m.specRegs
	result.FinalArchMemory = cloneMemory(m.archMem)
	result.FinalSpecMemory = cloneMemory(m.specMem)
	return result, nil
}

func (m *machine) store(cycle, pc int, addr Addr, value Value) {
	if len(m.storeBuffer) >= m.cfg.StoreBufferSize {
		m.retireOldest()
	}
	m.storeBuffer = append(m.storeBuffer, &storeEntry{
		id:         m.nextStoreID,
		pc:         pc,
		addr:       addr,
		value:      value,
		committed:  false,
		issueCycle: cycle,
	})
	m.nextStoreID++
}

func (m *machine) retireOldest() {
	if len(m.storeBuffer) == 0 {
		return
	}
	entry := m.storeBuffer[0]
	if !entry.committed {
		entry.committed = true
	}
	m.specMem[entry.addr] = entry.value
	m.storeBuffer = m.storeBuffer[1:]
}

func (m *machine) commitStores(cycle int) {
	for _, entry := range m.storeBuffer {
		if !entry.committed && cycle-entry.issueCycle >= m.cfg.StoreCommitDelay {
			entry.committed = true
		}
	}
}

func (m *machine) load(cycle, pc int, addr Addr, dst Reg) {
	predIndex := -1
	for i := len(m.storeBuffer) - 1; i >= 0; i-- {
		if m.storeBuffer[i].addr&0x3f == addr&0x3f {
			predIndex = i
			break
		}
	}

	archIndex := -1
	for i := len(m.storeBuffer) - 1; i >= 0; i-- {
		if m.storeBuffer[i].addr == addr {
			archIndex = i
			break
		}
	}

	memValue := m.specMem[addr]
	archValue := memValue
	if archIndex >= 0 {
		archValue = m.storeBuffer[archIndex].value
	}

	rec := &loadRecord{}
	rec.ID = m.nextLoadID
	m.nextLoadID++
	rec.PC = pc
	rec.Addr = addr
	rec.Dst = dst
	rec.IssueCycle = cycle
	rec.SelectedStore = -1
	rec.SelectedStorePC = -1
	rec.ArchStore = -1
	rec.ArchStorePC = -1
	rec.ArchValue = archValue
	if archIndex >= 0 {
		rec.ArchStore = m.storeBuffer[archIndex].id
		rec.ArchStorePC = m.storeBuffer[archIndex].pc
	}

	// A younger load to the same destination owns the register. An older
	// unresolved load must not overwrite it when it is eventually checked.
	if older := m.pendingReg[dst]; older != nil {
		older.superseded = true
		older.Superseded = true
		m.pendingReg[dst] = nil
	}

	if predIndex >= 0 {
		entry := m.storeBuffer[predIndex]
		rec.SelectedStore = entry.id
		rec.SelectedStorePC = entry.pc
		rec.SelectedStoreAddr = entry.addr
		rec.SelectedStoreValue = entry.value
		rec.SelectedStoreCommitted = entry.committed
		rec.SpecValue = entry.value
		rec.Forwarded = true
		rec.Mismatch = entry.addr != addr
		rec.ResolveCycle = cycle + m.cfg.ResolveDelay

		m.specRegs[dst] = entry.value
		m.pendingReg[dst] = rec
		m.pending = append(m.pending, rec)
	} else {
		rec.SpecValue = memValue
		rec.ResolveCycle = cycle
		rec.RegBeforeRollback = memValue
		rec.RegAfterRollback = memValue
		rec.resolved = true
		m.specRegs[dst] = memValue
	}

	m.allLoads = append(m.allLoads, rec)
}

func (m *machine) resolveDue(cycle int) {
	if len(m.pending) == 0 {
		return
	}
	dst := m.pending[:0]
	for _, rec := range m.pending {
		if !rec.resolved && rec.ResolveCycle <= cycle {
			m.resolve(rec, cycle, false)
		}
		if !rec.resolved {
			dst = append(dst, rec)
		}
	}
	m.pending = dst
}

func (m *machine) resolveAllAtBarrier(cycle int) {
	for _, rec := range m.pending {
		if !rec.resolved {
			m.resolve(rec, cycle, true)
		}
	}
	m.pending = nil
}

func (m *machine) resolve(rec *loadRecord, cycle int, atBarrier bool) {
	before := m.specRegs[rec.Dst]
	rec.RegBeforeRollback = before

	switch {
	case rec.superseded:
		rec.RegAfterRollback = before
		rec.RolledBack = false
	case rec.Mismatch:
		after := rec.ArchValue
		for _, imm := range rec.postXors {
			after ^= imm
		}
		m.specRegs[rec.Dst] = after
		rec.RegAfterRollback = after
		rec.RolledBack = true
	default:
		rec.RegAfterRollback = before
		rec.RolledBack = false
	}

	if m.pendingReg[rec.Dst] == rec {
		m.pendingReg[rec.Dst] = nil
	}
	rec.ResolveCycle = cycle
	rec.ResolvedAtBarrier = atBarrier
	rec.resolved = true
}

func (m *machine) snapshot(cycle, pc int, kind Kind, probe *ProbeRecord) CycleState {
	state := CycleState{
		Cycle:          cycle,
		PC:             pc,
		HasInstruction: pc >= 0,
		Kind:           kind,
		ArchRegs:       m.archRegs,
		SpecRegs:       m.specRegs,
	}
	state.StoreBuffer = make([]StoreSnapshot, 0, len(m.storeBuffer))
	for _, entry := range m.storeBuffer {
		state.StoreBuffer = append(state.StoreBuffer, StoreSnapshot{
			ID:         entry.id,
			PC:         entry.pc,
			Addr:       entry.addr,
			Value:      entry.value,
			Committed:  entry.committed,
			IssueCycle: entry.issueCycle,
		})
	}
	state.Pending = make([]PendingSnapshot, 0, len(m.pending))
	for _, rec := range m.pending {
		if rec.resolved {
			continue
		}
		state.Pending = append(state.Pending, PendingSnapshot{
			ID:           rec.ID,
			PC:           rec.PC,
			Dst:          rec.Dst,
			ResolveCycle: rec.ResolveCycle,
			Mismatch:     rec.Mismatch,
		})
	}
	if probe != nil {
		copy := *probe
		state.Probe = &copy
	}
	return state
}

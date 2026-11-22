package permverifier

import (
	"errors"
	"fmt"
	"math/big"
	"math/bits"
)

const (
	defaultMaxStates = 100_000
	defaultMaxSteps  = 200
)

// Verify proves or refutes staged-commit safety for one batch. Every linear
// interleaving consistent with the dependency DAG is explored. A safe report
// means no reachable configuration ever makes a physical byte simultaneously
// writable and executable; an unsafe report carries the shortest failing
// prefix and the first conflicting physical interval.
func Verify(batch []Op, opts Options) (*Report, error) {
	opts = withDefaults(opts)
	if err := validateBatch(batch, opts); err != nil {
		return nil, err
	}
	return explore(batch, opts), nil
}

func withDefaults(o Options) Options {
	if o.MaxStates <= 0 {
		o.MaxStates = defaultMaxStates
	}
	if o.MaxSteps <= 0 {
		o.MaxSteps = defaultMaxSteps
	}
	if o.Pool.Len == 0 {
		o.Pool = Interval{Lo: 0x4000_0000_0000_0000, Len: 1 << 40}
	}
	return o
}

func validateBatch(batch []Op, opts Options) error {
	if len(batch) == 0 {
		return errors.New("permverifier: empty batch")
	}
	ids := make(map[uint64]int, len(batch))
	for i, op := range batch {
		if err := validateOp(op); err != nil {
			return fmt.Errorf("permverifier: op %d: %w", i, err)
		}
		if prev, dup := ids[op.ID]; dup {
			return fmt.Errorf("permverifier: op id %d duplicated at %d and %d", op.ID, prev, i)
		}
		ids[op.ID] = i
	}
	for _, op := range batch {
		for _, dep := range op.Deps() {
			if _, ok := ids[dep]; !ok {
				return fmt.Errorf("permverifier: op %d depends on unknown op %d", op.ID, dep)
			}
		}
	}
	if err := detectCycle(batch, ids); err != nil {
		return err
	}
	if _, ok := opts.Pool.End(); !ok && opts.Pool.Lo != 0 {
		return errors.New("permverifier: pool interval overflows 64 bits")
	}
	if err := validatePoolBudget(batch, opts.Pool); err != nil {
		return err
	}
	return nil
}

// validatePoolBudget over-approximates private COW frame demand: each write
// can split at most ceil(writeLen/page) pages of one view-sized page table.
func validatePoolBudget(batch []Op, pool Interval) error {
	pageOf := map[uint64]uint64{}
	var bytes uint64
	for _, op := range batch {
		var m *MapOp
		switch {
		case op.Map != nil:
			m = op.Map
		case op.Remap != nil:
			m = &op.Remap.Map
		}
		if m != nil && m.Cow != nil {
			pageOf[m.View] = m.Cow.Page
		}
		if op.Fork != nil {
			if page, ok := pageOf[op.Fork.Parent]; ok {
				pageOf[op.Fork.View] = page
			}
		}
		if op.Write == nil {
			continue
		}
		page, ok := pageOf[op.Write.View]
		if !ok {
			continue
		}
		cost, carry := mul64(pagesFor(op.Write.Len, page), page)
		if carry {
			return errors.New("permverifier: COW demand overflows pool accounting")
		}
		bytes, carry = add64(bytes, cost)
		if carry || bytes > pool.Len {
			return errors.New("permverifier: private COW frame pool smaller than worst-case demand")
		}
	}
	return nil
}

func validateOp(op Op) error {
	payloads := 0
	if op.Alloc != nil {
		payloads++
	}
	if op.Free != nil {
		payloads++
	}
	if op.Map != nil {
		payloads++
	}
	if op.Remap != nil {
		payloads++
	}
	if op.Revoke != nil {
		payloads++
	}
	if op.Ack != nil {
		payloads++
	}
	if op.Fork != nil {
		payloads++
	}
	if op.Write != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("op %d must carry exactly one payload", op.ID)
	}
	switch {
	case op.Alloc != nil:
		return validateInterval(op.Alloc.Phys, "alloc.phys")
	case op.Free != nil:
		return validateInterval(op.Free.Phys, "free.phys")
	case op.Map != nil:
		return validateMap(op.Map)
	case op.Remap != nil:
		if err := validateMap(&op.Remap.Map); err != nil {
			return err
		}
		return validateDeps(op.Remap.Depends, "remap")
	case op.Revoke != nil:
		return validateDeps(op.Revoke.Depends, "revoke")
	case op.Ack != nil:
		return validateDeps(op.Ack.Depends, "ack")
	case op.Fork != nil:
		return validateFork(op.Fork)
	case op.Write != nil:
		return validateWrite(op.Write)
	default:
		return nil
	}
}

func validateInterval(i Interval, name string) error {
	if i.Empty() {
		return fmt.Errorf("%s must be non-empty", name)
	}
	end, ok := i.End()
	if !ok && end != 0 {
		return fmt.Errorf("%s overflows 64-bit address space", name)
	}
	return nil
}

func validateMap(m *MapOp) error {
	if err := validateDeps(m.Depends, "map"); err != nil {
		return err
	}
	if m.Perm == 0 {
		return errors.New("map requires permissions")
	}
	if m.Len == 0 {
		return errors.New("map length must be non-empty")
	}
	if m.Off > 0 && m.Len > mathMaxUint64-m.Off+1 {
		return errors.New("map logical range overflows 64 bits")
	}
	if len(m.CPUs) == 0 {
		return errors.New("map must name at least one CPU")
	}
	if hasDupU64(m.CPUs) {
		return errors.New("map CPU list contains duplicates")
	}
	if (m.Cow == nil) == (len(m.Segments) == 0) {
		return errors.New("map requires either segments or exactly one cow spec")
	}
	if m.Cow != nil {
		if m.Cow.Page == 0 || m.Cow.Len == 0 {
			return errors.New("cow page and length must be non-zero")
		}
		if m.Cow.Len != m.Len {
			return errors.New("cow length must match map length")
		}
		if m.Cow.PAddr > 0 && m.Cow.Len > mathMaxUint64-m.Cow.PAddr+1 {
			return errors.New("cow root region overflows 64 bits")
		}
	}
	if len(m.Segments) > 0 {
		if err := validateSegments(m.Off, m.Len, m.Segments); err != nil {
			return err
		}
	}
	return nil
}

func validateSegments(off, length uint64, segs []Segment) error {
	cursor := off
	for i, s := range segs {
		if s.Off != cursor {
			return fmt.Errorf("segment %d leaves a hole or reorders offsets", i)
		}
		if s.Len == 0 {
			return fmt.Errorf("segment %d is empty", i)
		}
		if s.PAddr > 0 && s.Len > mathMaxUint64-s.PAddr+1 {
			return fmt.Errorf("segment %d physical range overflows", i)
		}
		next, carry := add64(cursor, s.Len)
		if carry {
			return errors.New("segment chain overflows logical range")
		}
		cursor = next
	}
	if cursor != off+length {
		return errors.New("segments do not cover the declared logical range")
	}
	return nil
}

func validateFork(f *ForkOp) error {
	if err := validateDeps(f.Depends, "fork"); err != nil {
		return err
	}
	if f.Perm == 0 || f.Len == 0 {
		return errors.New("fork requires permissions and length")
	}
	if len(f.CPUs) == 0 || hasDupU64(f.CPUs) {
		return errors.New("fork needs a distinct non-empty CPU list")
	}
	if f.Off > 0 && f.Len > mathMaxUint64-f.Off+1 {
		return errors.New("fork logical range overflows 64 bits")
	}
	return nil
}

func validateWrite(wr *WriteOp) error {
	if err := validateDeps(wr.Depends, "write"); err != nil {
		return err
	}
	if wr.Len == 0 {
		return errors.New("write range must be non-empty")
	}
	if wr.Off > 0 && wr.Len > mathMaxUint64-wr.Off+1 {
		return errors.New("write range overflows 64 bits")
	}
	return nil
}

func validateDeps(deps []uint64, kind string) error {
	if hasDupU64(deps) {
		return fmt.Errorf("%s dependency list contains duplicates", kind)
	}
	return nil
}

func hasDupU64(xs []uint64) bool {
	seen := make(map[uint64]struct{}, len(xs))
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			return true
		}
		seen[x] = struct{}{}
	}
	return false
}

func detectCycle(batch []Op, index map[uint64]int) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]uint8, len(batch))
	var visit func(i int) error
	visit = func(i int) error {
		if color[i] == gray {
			return fmt.Errorf("permverifier: dependency cycle through op %d", batch[i].ID)
		}
		if color[i] == black {
			return nil
		}
		color[i] = gray
		for _, dep := range batch[i].Deps() {
			if err := visit(index[dep]); err != nil {
				return err
			}
		}
		color[i] = black
		return nil
	}
	for i := range batch {
		if err := visit(i); err != nil {
			return err
		}
	}
	return nil
}

type bfsNode struct {
	world *world
	mask  *big.Int
	path  []uint64
}

func explore(batch []Op, opts Options) *Report {
	index := make(map[uint64]int, len(batch))
	for i, op := range batch {
		index[op.ID] = i
	}
	root := &bfsNode{
		world: newWorld(opts.Pool),
		mask:  new(big.Int),
	}
	queue := []*bfsNode{root}
	visited := map[string]struct{}{}
	explored := 0
	incomplete := false
	incompleteReason := ""

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		key := node.mask.String()
		if _, ok := visited[key]; ok {
			continue
		}
		visited[key] = struct{}{}
		explored++
		if explored > opts.MaxStates {
			return &Report{Verdict: VerdictUnknown, StatesExplored: explored,
				Reason: "state budget exhausted"}
		}
		if c := node.world.firstConflict(); c != nil {
			return &Report{
				Verdict:        VerdictUnsafe,
				Conflict:       c,
				Prefix:         append([]uint64(nil), node.path...),
				StatesExplored: explored,
			}
		}
		if len(node.path) >= opts.MaxSteps && node.mask.BitLen() < len(batch) {
			return &Report{Verdict: VerdictUnknown, StatesExplored: explored,
				Reason: "step budget exhausted"}
		}
		anyEnabled := false
		terminal := popCountBig(node.mask) == len(batch)
		for i, op := range batch {
			if node.mask.Bit(i) == 1 {
				continue
			}
			if !depsSatisfied(node.mask, op, index) || !node.world.enabled(op) {
				continue
			}
			anyEnabled = true
			next := &bfsNode{
				world: node.world.clone(),
				mask:  new(big.Int).Set(node.mask),
				path:  append(append([]uint64(nil), node.path...), op.ID),
			}
			if !next.world.apply(op) {
				return &Report{Verdict: VerdictUnknown, StatesExplored: explored,
					Reason: "private COW frame pool exhausted"}
			}
			next.mask.SetBit(next.mask, i, 1)
			queue = append(queue, next)
		}
		if !anyEnabled && node.mask.BitLen() < len(batch) {
			incomplete = true
			incompleteReason = describeStuck(batch, node)
		}
		if terminal && len(node.world.revokes) > 0 {
			incomplete = true
			if incompleteReason == "" {
				incompleteReason = "revocation never fully acknowledged"
			}
		}
	}
	if incomplete {
		return &Report{Verdict: VerdictIncomplete, StatesExplored: explored,
			Reason: incompleteReason}
	}
	return &Report{Verdict: VerdictSafe, StatesExplored: explored}
}

func depsSatisfied(mask *big.Int, op Op, index map[uint64]int) bool {
	for _, dep := range op.Deps() {
		if mask.Bit(index[dep]) == 0 {
			return false
		}
	}
	return true
}

func popCountBig(m *big.Int) int {
	n := 0
	for _, word := range m.Bits() {
		n += bits.OnesCount(uint(word))
	}
	return n
}

func describeStuck(batch []Op, node *bfsNode) string {
	for i, op := range batch {
		if node.mask.Bit(i) == 1 {
			continue
		}
		if !node.world.enabled(op) {
			return fmt.Sprintf("op %d can never become enabled in some interleaving", op.ID)
		}
	}
	return "dependency dead end"
}

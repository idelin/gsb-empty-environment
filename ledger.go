package netsec

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
)

type planEntry struct {
	plan      Plan
	receipts  []*Receipt
	digestSet map[[32]byte]struct{}
	children  map[string]struct{}
}

// Ledger is the privacy host's plan state machine. It is safe for
// concurrent use.
type Ledger struct {
	mu      sync.RWMutex
	budget  int64
	plans   map[string]*planEntry
	roots   map[string]struct{}
	receipt map[string]*Receipt
	seq     int
}

// NewLedger creates a ledger capped by the given global privacy budget.
func NewLedger(budget int64) *Ledger {
	return &Ledger{
		budget:  budget,
		plans:   make(map[string]*planEntry),
		roots:   make(map[string]struct{}),
		receipt: make(map[string]*Receipt),
	}
}

// Budget returns the global budget the ledger was created with.
func (l *Ledger) Budget() int64 {
	return l.budget
}

// NewPlan reserves a root plan. A positive fixedSteps declares a fixed
// sequence; fixedSteps <= 0 creates an adaptive root plan.
func (l *Ledger) NewPlan(id string, reserve int64, fixedSteps int) (*Plan, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.plans[id]; ok {
		return nil, fmt.Errorf("netsec: plan id %q already exists", id)
	}
	if reserve < 0 {
		return nil, ErrBudgetExceeded
	}
	kind := Adaptive
	if fixedSteps > 0 {
		kind = Fixed
	} else if fixedSteps < 0 {
		return nil, ErrInvalidSteps
	}
	if l.rootReservedLocked()+reserve > l.budget {
		return nil, fmt.Errorf("%w: root reservation %d exceeds global budget", ErrBudgetExceeded, reserve)
	}

	p := &Plan{
		ID:        id,
		Kind:      kind,
		State:     Reserved,
		Reserve:   reserve,
		Steps:     fixedSteps,
		Available: reserve,
	}
	l.plans[id] = &planEntry{
		plan:      *p,
		digestSet: make(map[[32]byte]struct{}),
		children:  make(map[string]struct{}),
	}
	l.roots[id] = struct{}{}
	return clonePlan(p), nil
}

// OpenBranch reserves an adaptive child plan that forks from the published
// parent result parentReceiptID. The reservation is locked inside the
// parent plan and cannot exceed the parent's currently available budget.
func (l *Ledger) OpenBranch(id, parentID, parentReceiptID string, reserve int64) (*Plan, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.plans[id]; ok {
		return nil, fmt.Errorf("netsec: plan id %q already exists", id)
	}
	if reserve < 0 {
		return nil, ErrBudgetExceeded
	}
	parent, ok := l.plans[parentID]
	if !ok {
		return nil, fmt.Errorf("%w: parent %q", ErrUnknownPlan, parentID)
	}
	if parent.plan.Kind == Fixed {
		return nil, ErrFixedBranch
	}
	fork, ok := l.receipt[parentReceiptID]
	if !ok || fork.PlanID != parentID {
		return nil, fmt.Errorf("%w: %q", ErrNoParentResult, parentReceiptID)
	}
	if parent.plan.State != Active {
		return nil, fmt.Errorf("%w: parent %q is %s", ErrInvalidState, parentID, parent.plan.State)
	}
	if available := l.availableLocked(parentID); reserve > available {
		return nil, fmt.Errorf("%w: branch %d > parent available %d", ErrBudgetExceeded, reserve, available)
	}

	p := &Plan{
		ID:         id,
		Kind:       Adaptive,
		State:      Reserved,
		Reserve:    reserve,
		ParentID:   parentID,
		ParentRecv: parentReceiptID,
		Available:  reserve,
	}
	l.plans[id] = &planEntry{
		plan:      *p,
		digestSet: make(map[[32]byte]struct{}),
		children:  make(map[string]struct{}),
	}
	parent.children[id] = struct{}{}
	parent.plan.Available = l.availableLocked(parentID)
	return clonePlan(p), nil
}

// Publish charges cost to the plan and emits a receipt binding the parent
// result digest. Re-publishing the same receipt ID returns the stored
// receipt without charging again (idempotent retry).
func (l *Ledger) Publish(in PublishInput) (*Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if in.Cost <= 0 {
		return nil, ErrInvalidCost
	}
	if existing, ok := l.receipt[in.ReceiptID]; ok {
		return cloneReceipt(existing), nil
	}
	entry, ok := l.plans[in.PlanID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPlan, in.PlanID)
	}
	p := entry.plan
	if p.Kind == Fixed && p.Published >= p.Steps {
		return nil, fmt.Errorf("%w: plan %q declared %d steps", ErrStepCount, in.PlanID, p.Steps)
	}
	if p.State != Reserved && p.State != Active {
		return nil, fmt.Errorf("%w: plan %q is %s", ErrInvalidState, in.PlanID, p.State)
	}
	if _, dup := entry.digestSet[in.ResultDigest]; dup {
		return nil, fmt.Errorf("%w: result digest already used in plan %q", ErrDuplicateReceipt, in.PlanID)
	}
	if available := l.availableLocked(in.PlanID); in.Cost > available {
		return nil, fmt.Errorf("%w: cost %d > available %d", ErrCostExceeded, in.Cost, available)
	}

	step := p.Published + 1
	l.seq++
	receipt := &Receipt{
		ID:           in.ReceiptID,
		PlanID:       in.PlanID,
		Step:         step,
		Seq:          l.seq,
		Cost:         in.Cost,
		ResultDigest: in.ResultDigest,
	}
	if step == 1 {
		if p.ParentID == "" {
			receipt.ParentDigest = ZeroDigest
		} else {
			fork := l.receipt[p.ParentRecv]
			receipt.ParentDigest = fork.ResultDigest
			receipt.ParentPlan = p.ParentID
			receipt.ParentReceipt = p.ParentRecv
		}
	} else {
		prev := entry.receipts[len(entry.receipts)-1]
		receipt.ParentDigest = prev.ResultDigest
	}
	receipt.Path = l.buildPathLocked(p, receipt)

	entry.receipts = append(entry.receipts, receipt)
	entry.digestSet[in.ResultDigest] = struct{}{}
	l.receipt[in.ReceiptID] = receipt

	entry.plan.State = Active
	entry.plan.Spent += in.Cost
	entry.plan.Published = step
	entry.plan.Available = l.availableLocked(in.PlanID)
	if p.Kind == Fixed && step >= p.Steps {
		entry.plan.State = Completed
	}
	return cloneReceipt(receipt), nil
}

// Release returns the full reservation of an unpublished branch to its
// parent. Only a Reserved plan that never published can be released.
func (l *Ledger) Release(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.plans[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownPlan, id)
	}
	if entry.plan.State != Reserved {
		return fmt.Errorf("%w: plan %q is %s", ErrInvalidState, id, entry.plan.State)
	}
	entry.plan.State = Released
	entry.plan.Available = 0
	if entry.plan.ParentID != "" {
		parent := l.plans[entry.plan.ParentID]
		delete(parent.children, id)
		parent.plan.Available = l.availableLocked(parent.plan.ID)
	} else {
		delete(l.roots, id)
	}
	return nil
}

// Cancel stops a published adaptive plan. Amounts already spent are kept;
// the unspent reservation is forfeited and never refunded to the parent.
func (l *Ledger) Cancel(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.plans[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownPlan, id)
	}
	if entry.plan.State != Active {
		return fmt.Errorf("%w: plan %q is %s", ErrInvalidState, id, entry.plan.State)
	}
	if l.hasOpenChildrenLocked(entry) {
		return fmt.Errorf("%w: plan %q has open branches", ErrInvalidState, id)
	}
	entry.plan.State = Canceled
	entry.plan.Available = 0
	l.refreshParentLocked(entry.plan)
	return nil
}

// Finish marks an active adaptive plan completed once it has no open
// branches. Its unspent reservation is forfeited, like Cancel.
func (l *Ledger) Finish(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.plans[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownPlan, id)
	}
	if entry.plan.State != Active {
		return fmt.Errorf("%w: plan %q is %s", ErrInvalidState, id, entry.plan.State)
	}
	if l.hasOpenChildrenLocked(entry) {
		return fmt.Errorf("%w: plan %q has open branches", ErrInvalidState, id)
	}
	entry.plan.State = Completed
	entry.plan.Available = 0
	l.refreshParentLocked(entry.plan)
	return nil
}

func (l *Ledger) hasOpenChildrenLocked(entry *planEntry) bool {
	for childID := range entry.children {
		state := l.plans[childID].plan.State
		if state == Reserved || state == Active {
			return true
		}
	}
	return false
}

// refreshParentLocked recomputes the parent's available amount after a
// child terminates. Ending a child never refunds its unspent reserve; the
// parent only stops counting that child's lock once the child is gone.
func (l *Ledger) refreshParentLocked(p Plan) {
	if p.ParentID == "" {
		return
	}
	parent := l.plans[p.ParentID]
	parent.plan.Available = l.availableLocked(p.ParentID)
}

// Plan returns a snapshot of the named plan.
func (l *Ledger) Plan(id string) (*Plan, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	entry, ok := l.plans[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPlan, id)
	}
	return clonePlan(&entry.plan), nil
}

// Receipt returns the receipt with the given global id.
func (l *Ledger) Receipt(id string) (*Receipt, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	r, ok := l.receipt[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownReceipt, id)
	}
	return cloneReceipt(r), nil
}

// PlanReceipts returns a plan's receipts ordered by step.
func (l *Ledger) PlanReceipts(planID string) ([]*Receipt, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	entry, ok := l.plans[planID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPlan, planID)
	}
	out := make([]*Receipt, 0, len(entry.receipts))
	for _, r := range entry.receipts {
		out = append(out, cloneReceipt(r))
	}
	return out, nil
}

// VerifyReceipt checks that a receipt's parent digest binding and its full
// fork lineage match the ledger's stored records. A nil error means the
// receipt is internally consistent and correctly attributed.
func (l *Ledger) VerifyReceipt(id string) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	r, ok := l.receipt[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownReceipt, id)
	}
	entry := l.plans[r.PlanID]

	// Verify the in-plan parent digest (or the forked parent result).
	if r.Step == 1 {
		if entry.plan.ParentID == "" {
			if r.ParentDigest != ZeroDigest || r.ParentPlan != "" || r.ParentReceipt != "" {
				return fmt.Errorf("netsec: receipt %q root binding mismatch", id)
			}
		} else {
			fork, ok := l.receipt[entry.plan.ParentRecv]
			if !ok {
				return fmt.Errorf("%w: fork base %q", ErrUnknownReceipt, entry.plan.ParentRecv)
			}
			if r.ParentDigest != fork.ResultDigest ||
				r.ParentPlan != entry.plan.ParentID ||
				r.ParentReceipt != entry.plan.ParentRecv {
				return fmt.Errorf("netsec: receipt %q fork binding mismatch", id)
			}
		}
	} else {
		prev := entry.receipts[r.Step-2]
		if r.ParentDigest != prev.ResultDigest {
			return fmt.Errorf("netsec: receipt %q parent digest mismatch", id)
		}
	}

	// Verify the recorded lineage against the live receipt chain.
	if len(r.Path) == 0 {
		return fmt.Errorf("netsec: receipt %q has empty path", id)
	}
	last := r.Path[len(r.Path)-1]
	if last.PlanID != r.PlanID || last.ReceiptID != r.ID || last.Digest != r.ResultDigest {
		return fmt.Errorf("netsec: receipt %q path tail mismatch", id)
	}
	for _, hop := range r.Path {
		hopReceipt, ok := l.receipt[hop.ReceiptID]
		if !ok {
			return fmt.Errorf("%w: path receipt %q", ErrUnknownReceipt, hop.ReceiptID)
		}
		if hopReceipt.PlanID != hop.PlanID || hopReceipt.ResultDigest != hop.Digest {
			return fmt.Errorf("netsec: path hop %q mismatch", hop.ReceiptID)
		}
	}
	return nil
}

// Plans returns snapshots of all plans ordered by id.
func (l *Ledger) Plans() []*Plan {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]string, 0, len(l.plans))
	for id := range l.plans {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*Plan, 0, len(ids))
	for _, id := range ids {
		out = append(out, clonePlan(&l.plans[id].plan))
	}
	return out
}

// TotalSpent sums every charged cost across all plans. Duplicate publishes
// are idempotent, so retries never inflate this value.
func (l *Ledger) TotalSpent() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var total int64
	for _, r := range l.receipt {
		total += r.Cost
	}
	return total
}

// ReservedTotal sums reservations of every plan that is not Released.
func (l *Ledger) ReservedTotal() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var total int64
	for _, e := range l.plans {
		if e.plan.State != Released {
			total += e.plan.Reserve
		}
	}
	return total
}

// RootReserved sums reservations of non-released root plans. It never
// exceeds the global budget.
func (l *Ledger) RootReserved() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.rootReservedLocked()
}

func (l *Ledger) rootReservedLocked() int64 {
	var total int64
	for id := range l.roots {
		total += l.plans[id].plan.Reserve
	}
	return total
}

// availableLocked returns what a plan may still spend: its own reserve
// minus its own spend and minus reservations held by open child branches.
func (l *Ledger) availableLocked(id string) int64 {
	entry := l.plans[id]
	available := entry.plan.Reserve - entry.plan.Spent
	for childID := range entry.children {
		child := l.plans[childID]
		// A child keeps locking its full reservation unless it was released
		// before ever publishing. Canceled/completed branches forfeit their
		// unspent reserve and never refund the parent.
		if child.plan.State != Released {
			available -= child.plan.Reserve
		}
	}
	return available
}

// buildPathLocked records the full fork lineage, from the root receipt down
// to the receipt being published.
func (l *Ledger) buildPathLocked(p Plan, r *Receipt) []PathHop {
	var hops []PathHop
	if p.ParentID != "" {
		fork := l.receipt[p.ParentRecv]
		hops = append(hops, fork.Path...)
	}
	hops = append(hops, PathHop{
		PlanID:    r.PlanID,
		ReceiptID: r.ID,
		Step:      r.Step,
		Digest:    r.ResultDigest,
	})
	return hops
}

// Digest returns the SHA-256 digest of a raw query result. The host uses it
// to populate PublishInput.ResultDigest.
func Digest(data []byte) [32]byte {
	return sha256.Sum256(data)
}

func clonePlan(p *Plan) *Plan {
	cp := *p
	return &cp
}

func cloneReceipt(r *Receipt) *Receipt {
	cp := *r
	if r.Path != nil {
		cp.Path = append([]PathHop(nil), r.Path...)
	}
	return &cp
}

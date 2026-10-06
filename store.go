package receipt

import (
	"fmt"
	"sort"
	"sync"
)

type epochState struct {
	Head    Receipt
	HasHead bool
}

func (e *epochState) terminal() bool {
	return e.HasHead && e.Head.Status.Terminal()
}

// fold merges one receipt into the epoch head. The result depends only on
// the set of receipts, never on arrival order:
//   - a terminal receipt supersedes any non-terminal head;
//   - among terminal receipts the highest revision wins;
//   - among non-terminal receipts the highest revision wins;
//   - a non-terminal receipt never displaces a terminal head, so a terminal
//     epoch can never fall back to running/accepted.
func (e *epochState) fold(r Receipt) {
	if !e.HasHead {
		e.Head, e.HasHead = r, true
		return
	}
	head := e.Head
	if r.Revision == head.Revision {
		return
	}
	if e.terminal() {
		if r.Status.Terminal() && r.Revision > head.Revision {
			e.Head = r
		}
		return
	}
	if r.Status.Terminal() || r.Revision > head.Revision {
		e.Head = r
	}
}

type itemState struct {
	CurrentEpoch  uint64
	Epochs        map[uint64]*epochState
	Superseded    map[uint64]bool
	Buffered      map[uint64][]Receipt
	Evidence      map[string]Receipt
	Compensations []Compensation

	compMarked map[uint64]bool
}

func newItemState(epoch uint64) *itemState {
	return &itemState{
		CurrentEpoch: epoch,
		Epochs:       map[uint64]*epochState{epoch: {}},
		Superseded:   map[uint64]bool{},
		Buffered:     map[uint64][]Receipt{},
		Evidence:     map[string]Receipt{},
		compMarked:   map[uint64]bool{},
	}
}

func (it *itemState) rebuild() {
	it.compMarked = map[uint64]bool{}
	for _, c := range it.Compensations {
		it.compMarked[c.Epoch] = true
	}
	if it.Epochs == nil {
		it.Epochs = map[uint64]*epochState{}
	}
	if it.Superseded == nil {
		it.Superseded = map[uint64]bool{}
	}
	if it.Buffered == nil {
		it.Buffered = map[uint64][]Receipt{}
	}
	if it.Evidence == nil {
		it.Evidence = map[string]Receipt{}
	}
}

func evidenceKey(epoch, revision uint64) string {
	return fmt.Sprintf("%d/%d", epoch, revision)
}

func (it *itemState) epoch(e uint64) *epochState {
	es, ok := it.Epochs[e]
	if !ok {
		es = &epochState{}
		it.Epochs[e] = es
	}
	return es
}

func (it *itemState) markCompensation(itemID string, epoch uint64) {
	if it.compMarked[epoch] {
		return
	}
	it.compMarked[epoch] = true
	it.Compensations = append(it.Compensations, Compensation{
		ItemID: itemID,
		Epoch:  epoch,
		Status: StatusSucceeded,
	})
}

// foldReceipt records r as evidence and routes it: receipts for the current
// epoch advance the business head; receipts for a superseded epoch only
// update that epoch's history (a late success raises a pending
// compensation); receipts for a future epoch are buffered until a supersede
// decision commits it.
func (it *itemState) foldReceipt(r Receipt) {
	it.Evidence[evidenceKey(r.Epoch, r.Revision)] = r
	switch {
	case r.Epoch == it.CurrentEpoch:
		it.epoch(r.Epoch).fold(r)
	case it.Superseded[r.Epoch]:
		es := it.epoch(r.Epoch)
		es.fold(r)
		if es.HasHead && es.Head.Status == StatusSucceeded {
			it.markCompensation(r.ItemID, r.Epoch)
		}
	case r.Epoch > it.CurrentEpoch:
		it.Buffered[r.Epoch] = append(it.Buffered[r.Epoch], r)
	}
}

func (it *itemState) supersede(d SupersedeDecision) {
	it.Superseded[d.From] = true
	if es := it.Epochs[d.From]; es != nil && es.HasHead && es.Head.Status == StatusSucceeded {
		it.markCompensation(d.ItemID, d.From)
	}
	it.CurrentEpoch = d.To
	buffered := it.Buffered[d.To]
	delete(it.Buffered, d.To)
	for _, r := range buffered {
		it.foldReceipt(r)
	}
}

// Store is a receipt log keyed by item. The zero-value usage is New for
// in-memory or Open for a persistent, event-sourced store.
type Store struct {
	mu    sync.Mutex
	items map[string]*itemState
	seq   uint64
	log   *eventLog
	snap  string
}

// New returns an in-memory store.
func New() *Store {
	return &Store{items: map[string]*itemState{}}
}

// Register creates an item with its initial execution epoch.
func (s *Store) Register(itemID string, epoch uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[itemID]; ok {
		return ErrItemExists
	}
	if err := s.append(Event{Kind: kindRegister, ItemID: itemID, Epoch: epoch}); err != nil {
		return err
	}
	s.items[itemID] = newItemState(epoch)
	return nil
}

// Apply admits one receipt. Identical duplicates are no-ops; a receipt whose
// epoch/revision was already seen with different content returns ErrConflict.
func (s *Store) Apply(r Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !r.Status.valid() {
		return ErrInvalidStatus
	}
	it, ok := s.items[r.ItemID]
	if !ok {
		return ErrUnknownItem
	}
	if prev, ok := it.Evidence[evidenceKey(r.Epoch, r.Revision)]; ok {
		if prev.Status != r.Status || prev.Payload != r.Payload {
			return ErrConflict
		}
		return nil
	}
	if err := s.append(Event{Kind: kindReceipt, ItemID: r.ItemID, Receipt: &r}); err != nil {
		return err
	}
	it.foldReceipt(r)
	return nil
}

// Supersede commits a decision replacing the current epoch. The decision is
// valid only if From is the item's current epoch and To is greater.
func (s *Store) Supersede(d SupersedeDecision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[d.ItemID]
	if !ok {
		return ErrUnknownItem
	}
	if d.From != it.CurrentEpoch || d.To <= it.CurrentEpoch {
		return ErrStaleDecision
	}
	if err := s.append(Event{Kind: kindSupersede, ItemID: d.ItemID, Decision: &d}); err != nil {
		return err
	}
	it.supersede(d)
	return nil
}

// Head returns the business head of the item's current epoch.
func (s *Store) Head(itemID string) (Receipt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[itemID]
	if !ok {
		return Receipt{}, false
	}
	es := it.Epochs[it.CurrentEpoch]
	if es == nil || !es.HasHead {
		return Receipt{}, false
	}
	return es.Head, true
}

// CurrentEpoch returns the item's unique current epoch.
func (s *Store) CurrentEpoch(itemID string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[itemID]
	if !ok {
		return 0, false
	}
	return it.CurrentEpoch, true
}

// Evidence returns every admitted receipt, sorted by epoch and revision.
func (s *Store) Evidence(itemID string) []Receipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[itemID]
	if !ok {
		return nil
	}
	out := make([]Receipt, 0, len(it.Evidence))
	for _, r := range it.Evidence {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Epoch != out[j].Epoch {
			return out[i].Epoch < out[j].Epoch
		}
		return out[i].Revision < out[j].Revision
	})
	return out
}

// PendingCompensations lists side effects of superseded epochs that reached
// succeeded and still await compensation.
func (s *Store) PendingCompensations(itemID string) []Compensation {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[itemID]
	if !ok {
		return nil
	}
	out := make([]Compensation, len(it.Compensations))
	copy(out, it.Compensations)
	return out
}

// Buffered returns receipts held for epochs whose supersede decision has not
// committed yet.
func (s *Store) Buffered(itemID string) []Receipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[itemID]
	if !ok {
		return nil
	}
	var out []Receipt
	for _, rs := range it.Buffered {
		out = append(out, rs...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Epoch != out[j].Epoch {
			return out[i].Epoch < out[j].Epoch
		}
		return out[i].Revision < out[j].Revision
	})
	return out
}

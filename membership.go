package gracefulleave

import (
	"sort"
	"sync"
	"time"
)

// Membership is a node's local view of the cluster routing table.
//
// Transitions: Alive -> Leaving (on LeaveIntent) -> removed (on quiescent
// timer expiry). Removal is the "physical destroy" of the routing entry.
type Membership struct {
	mu        sync.RWMutex
	members   map[string]*Member
	quiescent time.Duration

	// afterFunc is injectable for deterministic tests; defaults to time.AfterFunc.
	afterFunc func(time.Duration, func()) *time.Timer
	// onRemove is an optional hook fired after an entry is physically removed.
	onRemove func(id string)
}

// NewMembership creates a routing table with the given quiescent period.
func NewMembership(quiescent time.Duration) *Membership {
	return &Membership{
		members:   make(map[string]*Member),
		quiescent: quiescent,
		afterFunc: time.AfterFunc,
	}
}

// SetOnRemove installs a hook invoked after an entry is physically removed.
func (ms *Membership) SetOnRemove(hook func(id string)) { ms.onRemove = hook }

// Add inserts or replaces a member entry.
func (ms *Membership) Add(m *Member) {
	ms.mu.Lock()
	ms.members[m.ID] = m
	ms.mu.Unlock()
}

// MarkLeaving transitions a member to StateLeaving and schedules the
// physical removal of its routing entry after the quiescent period.
// It returns false if the member is unknown or already leaving.
func (ms *Membership) MarkLeaving(id string) bool {
	ms.mu.RLock()
	m, ok := ms.members[id]
	ms.mu.RUnlock()
	if !ok {
		return false
	}
	if !m.markLeaving() {
		return false
	}
	ms.afterFunc(ms.quiescent, func() { ms.remove(id) })
	return true
}

// remove physically destroys the routing entry.
func (ms *Membership) remove(id string) {
	ms.mu.Lock()
	_, existed := ms.members[id]
	delete(ms.members, id)
	ms.mu.Unlock()
	if existed && ms.onRemove != nil {
		ms.onRemove(id)
	}
}

// Get returns the entry for id, if present.
func (ms *Membership) Get(id string) (*Member, bool) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	m, ok := ms.members[id]
	return m, ok
}

// Ready returns the ready routing pool: alive members only, sorted by ID
// for deterministic selection. Leaving members are excluded so no new
// traffic is routed to them during the quiescent period.
func (ms *Membership) Ready() []*Member {
	ms.mu.RLock()
	out := make([]*Member, 0, len(ms.members))
	for _, m := range ms.members {
		if m.State() == StateAlive {
			out = append(out, m)
		}
	}
	ms.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Len returns the number of entries (alive or leaving) still in the table.
func (ms *Membership) Len() int {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return len(ms.members)
}

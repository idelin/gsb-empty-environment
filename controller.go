package hotring

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrNoStandby is returned by Flip when no warmed-up standby ring exists.
var ErrNoStandby = errors.New("hotring: no warmed-up standby ring to flip to")

// MigrationReport describes the key-migration impact of switching from
// the active ring to the standby ring. It is precomputed during Warmup,
// before any traffic is moved.
type MigrationReport struct {
	OldTopology []string       // node set of the active ring
	NewTopology []string       // node set of the standby ring
	SampledKeys int            // keys examined during warmup
	MovedKeys   int            // keys whose owner changes after the flip
	GainedBy    map[string]int // keys gained per node
	LostBy      map[string]int // keys lost per node
}

// MovedRatio is the fraction of sampled keys that will be remapped.
func (r *MigrationReport) MovedRatio() float64 {
	if r.SampledKeys == 0 {
		return 0
	}
	return float64(r.MovedKeys) / float64(r.SampledKeys)
}

// Controller is a dual-ring hot-standby routing controller.
//
//   - The Active ring carries 100% of traffic. Route() reads it through
//     an atomic value: lock-free, allocation-free, safe for any number
//     of concurrent readers.
//   - The Standby ring is rebuilt in the background (PrepareStandby) and
//     its key-migration impact precomputed (Warmup) without disturbing
//     live traffic.
//   - Flip performs a single atomic pointer swap, switching all traffic
//     to the standby ring instantly. Readers in flight keep using the
//     (immutable) old ring, so no request ever observes a torn state.
type Controller struct {
	active atomic.Value // holds *Ring; written only by Flip/NewController

	mu      sync.Mutex       // serializes standby construction/warmup/flip
	standby *Ring            // ring under construction, nil until PrepareStandby
	report  *MigrationReport // warmup result, nil until Warmup
	warmed  bool

	flips    int64 // total completed flips (atomic)
	lastFlip int64 // unix-nano timestamp of last flip (atomic)
}

// NewController creates a controller whose active ring serves the given
// topology immediately.
func NewController(nodes []string, replicas int) *Controller {
	c := &Controller{}
	c.active.Store(NewRing(nodes, replicas))
	return c
}

// Route resolves key against the current active ring. It never blocks
// on standby reconstruction or on a concurrent Flip.
func (c *Controller) Route(key string) (string, error) {
	return c.ActiveRing().Get(key)
}

// ActiveRing returns the ring currently serving traffic.
func (c *Controller) ActiveRing() *Ring {
	return c.active.Load().(*Ring)
}

// PrepareStandby syncs a new topology into the standby ring in the
// background. Any previous un-flipped standby state is discarded.
// The active ring and live traffic are untouched.
func (c *Controller) PrepareStandby(nodes []string) {
	replicas := c.ActiveRing().Replicas()
	standby := NewRing(nodes, replicas) // expensive build happens off the lock
	c.mu.Lock()
	c.standby = standby
	c.report = nil
	c.warmed = false
	c.mu.Unlock()
}

// StandbyRing returns the ring currently staged as standby, or nil.
func (c *Controller) StandbyRing() *Ring {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.standby
}

// Warmup precomputes the key-migration impact of flipping to the
// standby ring by replaying the given known/sampled keys against both
// rings. Live traffic keeps flowing through the active ring.
func (c *Controller) Warmup(keys []string) (*MigrationReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.standby == nil {
		return nil, ErrNoStandby
	}
	active := c.ActiveRing()
	report := &MigrationReport{
		OldTopology: active.Members(),
		NewTopology: c.standby.Members(),
		SampledKeys: len(keys),
		GainedBy:    make(map[string]int),
		LostBy:      make(map[string]int),
	}
	for _, key := range keys {
		from, err1 := active.Get(key)
		to, err2 := c.standby.Get(key)
		if err1 != nil || err2 != nil || from == to {
			continue
		}
		report.MovedKeys++
		report.LostBy[from]++
		report.GainedBy[to]++
	}
	c.report = report
	c.warmed = true
	return report, nil
}

// MigrationReport returns the most recent warmup report, or nil.
func (c *Controller) MigrationReport() *MigrationReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.report
}

// Flip atomically promotes the warmed-up standby ring to active. The
// swap is a single atomic store: concurrent Route calls see either the
// old ring or the new ring, never a mixture, so there is zero transient
// misrouting. The retired ring stays immutable and is garbage-collected
// once in-flight readers drain.
func (c *Controller) Flip() (*MigrationReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.standby == nil || !c.warmed {
		return nil, ErrNoStandby
	}
	c.active.Store(c.standby) // the one-pointer global switch
	report := c.report
	c.standby = nil
	c.report = nil
	c.warmed = false
	atomic.AddInt64(&c.flips, 1)
	atomic.StoreInt64(&c.lastFlip, nowUnixNano())
	return report, nil
}

// Stats returns the total number of flips and the unix-nano timestamp
// of the most recent one (0 if never flipped).
func (c *Controller) Stats() (flips int64, lastFlipUnixNano int64) {
	return atomic.LoadInt64(&c.flips), atomic.LoadInt64(&c.lastFlip)
}

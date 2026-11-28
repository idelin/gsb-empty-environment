package engine

import (
	"errors"
	"fmt"
	"sync"

	"prescreen.local/ps-3e28dd0313/zns/device"
)

// Heat classifies how often a logical block is overwritten.
type Heat int

const (
	// HeatCold marks write-once / rarely overwritten data.
	HeatCold Heat = iota
	// HeatHot marks frequently overwritten data.
	HeatHot
)

// String returns the heat name.
func (h Heat) String() string {
	if h == HeatHot {
		return "HOT"
	}
	return "COLD"
}

// GCMode selects how logical blocks are routed to append zones.
type GCMode int

const (
	// ModeTiered maintains separate HOT and COLD zone pools so cold blocks are
	// not invalidated by churn inside the same zone.
	ModeTiered GCMode = iota
	// ModeMixed routes every write through a single shared pool, modelling the
	// naive engine that suffers from hot/cold co-location write amplification.
	ModeMixed
)

// Loc is the logical->physical mapping value: where a logical block currently
// lives on flash.
type Loc struct {
	ZoneID int
	Offset int64 // offset relative to the zone SLBA
	Heat   Heat
}

// absoluteLBA reconstructs the device LBA from a mapping entry.
func (l Loc) absoluteLBA(zoneSize int64) int64 {
	return int64(l.ZoneID)*zoneSize + l.Offset
}

// GCStats reports engine counters. WAF is computed as flash bytes (host writes
// plus GC migrations) divided by bytes the host asked to write.
type GCStats struct {
	HostWrites     int64
	FlashWrites    int64
	GCMigrations   int64
	GCRuns         int64
	GCBlocksStale  int64 // entries skipped because a newer version existed
	ZonesReset     int64
	ActiveMappings int
	Invalidated    int64 // blocks invalidated by overwrites
}

// WAF returns the write amplification factor, or 1.0 when nothing was written.
func (s GCStats) WAF() float64 {
	if s.HostWrites == 0 {
		return 1
	}
	return float64(s.FlashWrites) / float64(s.HostWrites)
}

// GCHook is invoked for every physical block considered during GC. It is the
// deterministic test seam for interleaving a concurrent overwrite with a GC
// pass; production callers leave it nil.
type GCHook func(victimZoneID int, logicalLBA int64, stale bool)

// Config configures the host GC engine.
type Config struct {
	// Device is the underlying emulated ZNS namespace.
	Device *device.Device
	// Mode selects hot/cold tiering versus a single mixed pool.
	Mode GCMode
	// AutoHotThreshold is the overwrite count (>=) at which an LBA is treated as
	// hot. Zero defaults to 2, i.e. the first overwrite promotes the block.
	AutoHotThreshold int
	// GCTriggerEmptyZones starts a GC pass whenever the number of EMPTY zones
	// drops to this value while a new zone is needed. Zero defaults to 1, which
	// always keeps one empty destination zone in reserve.
	GCTriggerEmptyZones int
	// Hook, when set, is called for each block scanned during GC.
	Hook GCHook
}

type zonePool struct {
	target int // currently open zone id, -1 when none
}

// Engine implements the host-side logical block map, heat-aware append routing
// and atomic compare-and-swap garbage collection on top of device.Device.
type Engine struct {
	dev         *device.Device
	mode        GCMode
	hot         int
	gcWatermark int
	hook        GCHook

	mu sync.RWMutex

	mapping    map[int64]Loc
	heatCount  map[int64]int
	pools      map[Heat]*zonePool
	zoneHeat   map[int]Heat
	zoneOwners map[int]map[int64]struct{}
	stats      GCStats
}

// New constructs an engine over the given device.
func New(cfg Config) (*Engine, error) {
	if cfg.Device == nil {
		return nil, fmt.Errorf("%w: nil device", ErrInvalidConfig)
	}
	threshold := cfg.AutoHotThreshold
	if threshold <= 0 {
		threshold = 2
	}
	watermark := cfg.GCTriggerEmptyZones
	if watermark <= 0 {
		watermark = 1
	}
	e := &Engine{
		dev:         cfg.Device,
		mode:        cfg.Mode,
		hot:         threshold,
		gcWatermark: watermark,
		hook:        cfg.Hook,
		mapping:     make(map[int64]Loc),
		heatCount:   make(map[int64]int),
		pools:       make(map[Heat]*zonePool),
		zoneHeat:    make(map[int]Heat),
		zoneOwners:  make(map[int]map[int64]struct{}),
	}
	e.pools[HeatHot] = &zonePool{target: -1}
	e.pools[HeatCold] = &zonePool{target: -1}
	return e, nil
}

// Mode reports the routing mode.
func (e *Engine) Mode() GCMode { return e.mode }

// classifyHeat bumps the overwrite counter and derives the heat of lba. In mixed
// mode everything is forced through the cold pool regardless of observed heat.
func (e *Engine) classifyHeat(lba int64) Heat {
	if e.mode == ModeMixed {
		return HeatCold
	}
	e.heatCount[lba]++
	if e.heatCount[lba] >= e.hot {
		return HeatHot
	}
	return HeatCold
}

// findEmptyLocked returns the lowest-id EMPTY zone, or -1.
func (e *Engine) findEmptyLocked() int {
	for _, info := range e.dev.Zones() {
		if info.State == device.ZoneEmpty {
			return info.ID
		}
	}
	return -1
}

// countEmptyLocked reports how many zones are currently EMPTY, including zones
// returned by a previous Zone Reset (they are reusable append destinations).
func (e *Engine) countEmptyLocked() int {
	n := 0
	for _, info := range e.dev.Zones() {
		if info.State == device.ZoneEmpty {
			n++
		}
	}
	return n
}

// takeEmptyLocked binds an EMPTY zone (fresh or reset) to a heat pool.
func (e *Engine) takeEmptyLocked(heat Heat) (int, bool) {
	id := e.findEmptyLocked()
	if id < 0 {
		return -1, false
	}
	e.pools[heat].target = id
	e.zoneHeat[id] = heat
	if _, exists := e.zoneOwners[id]; !exists {
		e.zoneOwners[id] = make(map[int64]struct{})
	}
	return id, true
}

// openTargetLocked returns a zone that can accept an append for the given heat,
// reusing the pool's open zone, a reset EMPTY zone or opening a fresh EMPTY
// zone. The optional spill pool is used when the preferred pool is saturated,
// which keeps GC migration always making progress.
func (e *Engine) openTargetLocked(preferred Heat, allowSpill bool) (int, Heat, error) {
	try := func(heat Heat) (int, bool) {
		pool := e.pools[heat]
		if pool.target >= 0 {
			info, err := e.dev.Zone(pool.target)
			if err == nil && info.State.IsWritable() && info.WritePointer < info.SLBA+info.ZoneCapacity {
				return pool.target, true
			}
			pool.target = -1
		}
		return e.takeEmptyLocked(heat)
	}

	if id, ok := try(preferred); ok {
		return id, preferred, nil
	}
	if allowSpill {
		other := HeatCold
		if preferred == HeatCold {
			other = HeatHot
		}
		if id, ok := try(other); ok {
			return id, other, nil
		}
	}
	return 0, preferred, ErrNoSpace
}

// Write appends one logical block at lba, routing it to the heat-appropriate
// zone. Any previous physical copy is invalidated atomically with the map swap.
func (e *Engine) Write(lba int64, payload []byte) (Loc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.writeLocked(lba, payload, e.classifyHeat(lba), false)
}

// writeLocked appends a block whose heat has already been classified, so GC
// retries never double-count overwrite frequency.
func (e *Engine) writeLocked(lba int64, payload []byte, heat Heat, allowGCTrigger bool) (Loc, error) {
	zoneID, usedHeat, err := e.openTargetLocked(heat, false)
	if err != nil {
		if !(allowGCTrigger && errors.Is(err, ErrNoSpace)) {
			return Loc{}, err
		}
		if gcErr := e.reclaimUntilSpaceLocked(heat); gcErr != nil {
			return Loc{}, gcErr
		}
		zoneID, usedHeat, err = e.openTargetLocked(heat, false)
		if err != nil {
			return Loc{}, err
		}
	}

	// Proactive watermark: reclaim FULL zones until an EMPTY destination remains
	// in reserve, so later writes (and GC itself) never dead-end.
	if allowGCTrigger {
		if e.countEmptyLocked() <= e.gcWatermark {
			if gcErr := e.reclaimUntilSpaceLocked(usedHeat); gcErr != nil {
				return Loc{}, gcErr
			}
			// Reclaiming may have moved our pool's target; re-resolve it.
			zoneID, usedHeat, err = e.openTargetLocked(heat, false)
			if err != nil {
				return Loc{}, err
			}
		}
	}

	assigned, err := e.dev.ZoneAppend(zoneID, 1, payload)
	if err != nil {
		return Loc{}, err
	}
	info, err := e.dev.Zone(zoneID)
	if err != nil {
		return Loc{}, err
	}
	loc := Loc{
		ZoneID: zoneID,
		Offset: assigned - info.SLBA,
		Heat:   usedHeat,
	}

	if old, ok := e.mapping[lba]; ok {
		e.invalidateLocked(lba, old)
	}
	e.mapping[lba] = loc
	e.zoneOwners[zoneID][lba] = struct{}{}
	e.stats.HostWrites++
	e.stats.FlashWrites++
	e.stats.ActiveMappings = len(e.mapping)
	return loc, nil
}

// reclaimUntilSpaceLocked runs GC passes until the preferred pool has a
// writable zone and the EMPTY-zone reserve is restored. It stops cleanly when
// no FULL victim remains, letting the caller surface ErrNoSpace.
func (e *Engine) reclaimUntilSpaceLocked(preferred Heat) error {
	const maxPasses = 128
	for pass := 0; pass < maxPasses; pass++ {
		if e.countEmptyLocked() > e.gcWatermark {
			if _, _, err := e.openTargetLocked(preferred, false); err == nil {
				return nil
			}
		}
		if _, err := e.gcLocked(-1); err != nil {
			if errors.Is(err, ErrNothingToGC) {
				return ErrNoSpace
			}
			return err
		}
	}
	return ErrNoSpace
}

// invalidateLocked removes lba from the owner set of the zone holding its stale
// copy. The device payload itself is only reclaimed when the zone resets.
func (e *Engine) invalidateLocked(lba int64, old Loc) {
	if owners, ok := e.zoneOwners[old.ZoneID]; ok {
		delete(owners, lba)
	}
	e.stats.Invalidated++
}

// WriteWithGC writes lba, running GC once whenever the preferred pool has no
// writable zone. It is the convenience path used by space-constrained tests.
func (e *Engine) WriteWithGC(lba int64, payload []byte) (Loc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	const maxAttempts = 64
	heat := e.classifyHeat(lba)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		loc, err := e.writeLocked(lba, payload, heat, true)
		if err != nil {
			return Loc{}, err
		}
		return loc, nil
	}
	return Loc{}, fmt.Errorf("%w: GC could not free capacity after retries", ErrNoSpace)
}

// Read resolves the current mapping and reads the payload from flash.
func (e *Engine) Read(lba int64) ([]byte, error) {
	e.mu.RLock()
	loc, ok := e.mapping[lba]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrUnknownLBA, lba)
	}
	info, err := e.dev.Zone(loc.ZoneID)
	if err != nil {
		return nil, err
	}
	return e.dev.Read(loc.absoluteLBA(info.ZoneSize))
}

// Lookup returns the current physical location of a logical block.
func (e *Engine) Lookup(lba int64) (Loc, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	loc, ok := e.mapping[lba]
	return loc, ok
}

// Stats returns a copy of the engine counters.
func (e *Engine) Stats() GCStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	stats := e.stats
	return stats
}

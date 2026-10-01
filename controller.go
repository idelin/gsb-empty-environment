package migration

import (
	"errors"
	"fmt"
	"hash/crc32"
	"sync"
	"sync/atomic"
)

// Generation identifies which topology generation owns a key.
type Generation int

const (
	// GenOld is the legacy topology (Top_Old).
	GenOld Generation = iota
	// GenNew is the target topology (Top_New).
	GenNew
)

func (g Generation) String() string {
	if g == GenNew {
		return "Top_New"
	}
	return "Top_Old"
}

// Stats reports how many requests were served by each topology generation.
type Stats struct {
	OldHits uint64
	NewHits uint64
}

// Controller progressively migrates traffic from Top_Old to Top_New.
//
// Routing model:
//   - A stable bucket (crc32(key) mod 100) plus the current traffic
//     percentage decides which generation a key SHOULD live on.
//   - A key-version registry records which generation a key CURRENTLY
//     lives on. Reads are routed by the recorded version, so a key is
//     never read from both topologies.
//   - When desired and recorded versions differ, the key is migrated
//     (read from the owning topology, write into the target topology,
//     flip the version) atomically under a per-key striped lock before
//     the request is served. A key therefore always has exactly one
//     write channel: dual-write split-brain is impossible.
type Controller struct {
	old *Topology
	new *Topology

	mu      sync.RWMutex // guards percent and newTopo publication
	percent int
	hasNew  bool

	vMu      sync.RWMutex // guards versions
	versions map[string]Generation

	stripes [64]sync.Mutex // per-key striped locks

	oldHits atomic.Uint64
	newHits atomic.Uint64
}

// NewController creates a controller serving 100% of traffic from Top_Old.
func NewController(oldTopo *Topology) (*Controller, error) {
	if oldTopo == nil {
		return nil, errors.New("old topology is required")
	}
	return &Controller{
		old:      oldTopo,
		versions: make(map[string]Generation),
	}, nil
}

// PublishNewTopology publishes the new cluster configuration (Top_New).
// Traffic is not shifted until SetTrafficPercent is called.
func (c *Controller) PublishNewTopology(newTopo *Topology) error {
	if newTopo == nil {
		return errors.New("new topology is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.new = newTopo
	c.hasNew = true
	return nil
}

// SetTrafficPercent sets the percentage (0-100) of the key space whose
// desired generation is Top_New. The remaining keys stay on Top_Old.
func (c *Controller) SetTrafficPercent(p int) error {
	if p < 0 || p > 100 {
		return fmt.Errorf("percent must be in [0,100], got %d", p)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if p > 0 && !c.hasNew {
		return errors.New("new topology not published")
	}
	c.percent = p
	return nil
}

// TrafficPercent returns the current canary percentage.
func (c *Controller) TrafficPercent() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.percent
}

// VerifyNewCluster probes every node of Top_New with a write/read/delete
// round-trip. Call it before (and during) traffic shifting.
func (c *Controller) VerifyNewCluster() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasNew {
		return errors.New("new topology not published")
	}
	probeKey, probeVal := "__probe__", "ok"
	for _, n := range c.new.Nodes() {
		n.Set(probeKey, probeVal)
		v, ok := n.Get(probeKey)
		if !ok || v != probeVal {
			return fmt.Errorf("new cluster node %q failed read/write probe", n.ID())
		}
		n.Delete(probeKey)
	}
	return nil
}

// bucket is the stable placement bucket of a key in [0,100).
func bucket(key string) int {
	return int(crc32.ChecksumIEEE([]byte(key)) % 100)
}

// desiredGen returns the generation the key should live on under the
// current traffic percentage.
func (c *Controller) desiredGen(key string) Generation {
	c.mu.RLock()
	p := c.percent
	c.mu.RUnlock()
	if bucket(key) < p {
		return GenNew
	}
	return GenOld
}

func (c *Controller) topoOf(g Generation) *Topology {
	if g == GenNew {
		return c.new
	}
	return c.old
}

func (c *Controller) keyLock(key string) *sync.Mutex {
	return &c.stripes[crc32.ChecksumIEEE([]byte(key))%uint32(len(c.stripes))]
}

// ownerOf returns the generation currently owning the key.
func (c *Controller) ownerOf(key string) Generation {
	c.vMu.RLock()
	defer c.vMu.RUnlock()
	if g, ok := c.versions[key]; ok {
		return g
	}
	return GenOld // unwritten keys default to the old generation
}

// reconcile migrates the key to its desired generation if needed.
// Must be called with the key's striped lock held.
func (c *Controller) reconcile(key string) Generation {
	desired := c.desiredGen(key)
	owner := c.ownerOf(key)
	if owner == desired {
		return owner
	}
	// Move existing data to the target topology, then flip the version.
	// The version flip is the single atomic hand-off of the write channel.
	if v, ok := c.topoOf(owner).Get(key); ok {
		c.topoOf(desired).Set(key, v)
		c.topoOf(owner).Delete(key)
	}
	c.vMu.Lock()
	c.versions[key] = desired
	c.vMu.Unlock()
	return desired
}

func (c *Controller) recordHit(g Generation) {
	if g == GenNew {
		c.newHits.Add(1)
	} else {
		c.oldHits.Add(1)
	}
}

// Get reads a key through the controller. The key is transparently
// migrated if its owning generation lags behind the desired one.
func (c *Controller) Get(key string) (string, bool) {
	lk := c.keyLock(key)
	lk.Lock()
	defer lk.Unlock()
	g := c.reconcile(key)
	c.recordHit(g)
	return c.topoOf(g).Get(key)
}

// Set writes a key through the controller. The write always lands on
// exactly one topology: the key's current (post-reconcile) generation.
func (c *Controller) Set(key, value string) {
	lk := c.keyLock(key)
	lk.Lock()
	defer lk.Unlock()
	g := c.reconcile(key)
	c.recordHit(g)
	c.topoOf(g).Set(key, value)
}

// Delete removes a key from whichever topology owns it.
func (c *Controller) Delete(key string) {
	lk := c.keyLock(key)
	lk.Lock()
	defer lk.Unlock()
	g := c.reconcile(key)
	c.recordHit(g)
	c.topoOf(g).Delete(key)
	c.vMu.Lock()
	delete(c.versions, key)
	c.vMu.Unlock()
}

// Stats returns cumulative per-generation request counts.
func (c *Controller) Stats() Stats {
	return Stats{OldHits: c.oldHits.Load(), NewHits: c.newHits.Load()}
}

// OwnerOf exposes the current owning generation of a key (for tests/demos).
func (c *Controller) OwnerOf(key string) Generation { return c.ownerOf(key) }

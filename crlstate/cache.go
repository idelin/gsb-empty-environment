package crlstate

import (
	"fmt"
	"sync"
	"time"
)

// CacheStats is a snapshot of cache activity.
type CacheStats struct {
	Entries      uint64
	Hits         uint64
	Misses       uint64
	Invalidated  uint64
	Invalidation uint64 // number of invalidation events
}

type cacheEntry struct {
	key      string
	cert     Certificate
	required ReasonMask
	serialLo Serial
	serialHi Serial
	timeLo   time.Time
	timeHi   time.Time
	result   Result
	seq      uint64
	version  uint64
}

type cacheNode struct {
	entry    *cacheEntry
	priority uint64
	left     *cacheNode
	right    *cacheNode
	maxHi    Serial
}

type issuerIndex struct {
	root  *cacheNode
	byKey map[string]*cacheEntry
	count int
}

// Cache is a validation result cache whose entries are invalidated precisely
// by the serial×reason×time domain of newly arrived manifests.
type Cache struct {
	agg *Aggregator

	mu         sync.Mutex
	issuers    map[IssuerID]*issuerIndex
	generation map[IssuerID]uint64
	nextSeq    uint64
	stats      CacheStats

	unsubscribe func()
}

// NewCache creates a cache and subscribes to agg's incremental change stream.
func NewCache(agg *Aggregator) *Cache {
	c := &Cache{
		agg:        agg,
		issuers:    make(map[IssuerID]*issuerIndex),
		generation: make(map[IssuerID]uint64),
	}
	c.unsubscribe = agg.Subscribe(c.applyChange)
	return c
}

// Close unsubscribes from the aggregator.
func (c *Cache) Close() {
	if c.unsubscribe != nil {
		c.unsubscribe()
		c.unsubscribe = nil
	}
}

func (c *Cache) index(issuer IssuerID) *issuerIndex {
	idx := c.issuers[issuer]
	if idx == nil {
		idx = &issuerIndex{byKey: make(map[string]*cacheEntry)}
		c.issuers[issuer] = idx
	}
	return idx
}

func normalizeRequired(m ReasonMask) ReasonMask {
	if m == 0 {
		return AllStatusReasons
	}
	return m
}

func cacheKeyFor(c Certificate, required ReasonMask) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d|%d",
		c.IssuerID, c.Serial, c.Serial.Add(1), c.Start.UnixNano(), c.End.UnixNano(), uint64(required))
}

// Check returns a cached or freshly computed status decision.
func (c *Cache) Check(req Certificate) Result {
	required := normalizeRequired(req.Reasons)
	if req.IssuerID == "" || !req.Start.Before(req.End) || required&^AllStatusReasons != 0 {
		return c.agg.Check(req)
	}
	key := cacheKeyFor(req, required)

	c.mu.Lock()
	idx := c.index(req.IssuerID)
	gen := c.generation[req.IssuerID]
	if e, ok := idx.byKey[key]; ok {
		if e.version == c.agg.Version(req.IssuerID) {
			c.stats.Hits++
			res := e.result.Clone()
			c.mu.Unlock()
			return res
		}
		// The change callback may not have run yet; evict immediately.
		idx.root = treapErase(idx.root, e)
		delete(idx.byKey, e.key)
		idx.count--
		c.recountLocked()
	}
	c.stats.Misses++
	c.mu.Unlock()

	res := c.agg.Check(req)
	if res.Status == StatusLocked {
		return res
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Do not publish a result computed across an invalidation boundary.
	version := c.agg.Version(req.IssuerID)
	if c.generation[req.IssuerID] != gen {
		return res
	}
	idx = c.index(req.IssuerID)
	if _, exists := idx.byKey[key]; !exists {
		c.nextSeq++
		e := &cacheEntry{
			key:      key,
			cert:     req,
			required: required,
			serialLo: req.Serial,
			serialHi: req.Serial.Add(1),
			timeLo:   req.Start,
			timeHi:   req.End,
			result:   res.Clone(),
			seq:      c.nextSeq,
			version:  version,
		}
		idx.root = treapInsert(idx.root, e)
		idx.byKey[key] = e
		idx.count++
		c.recountLocked()
	}
	return res
}

func (c *Cache) applyChange(ch Change) {
	switch ch.Kind {
	case ChangeManifest:
		if ch.Manifest == nil {
			c.invalidateIssuer(ch.IssuerID)
			return
		}
		from := ch.InvalidateFrom
		if from.IsZero() {
			from = ch.Manifest.ValidFrom
		}
		until := ch.InvalidateUntil
		if until.IsZero() {
			until = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		}
		c.invalidateDomain(ch.IssuerID, ch.Manifest.SerialLo, ch.Manifest.SerialHi,
			ch.Manifest.Reasons, from, until)
	case ChangeMigration:
		// A migration can connect arbitrary lifetime prefixes to its new
		// branch; migrations are rare, so issuer-wide invalidation keeps the
		// O(affected) promise for the high-volume manifest path.
		c.invalidateIssuer(ch.IssuerID)
	case ChangeLock:
		c.invalidateIssuer(ch.IssuerID)
	}
}

func (c *Cache) invalidateIssuer(issuer IssuerID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.issuers[issuer]
	if idx == nil {
		return
	}
	n := idx.count
	c.issuers[issuer] = &issuerIndex{byKey: make(map[string]*cacheEntry)}
	c.generation[issuer]++
	c.stats.Invalidated += uint64(n)
	c.stats.Invalidation++
	c.recountLocked()
}

func (c *Cache) invalidateDomain(issuer IssuerID, lo, hi Serial, reasons ReasonMask, tlo, thi time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.issuers[issuer]
	if idx == nil {
		return
	}
	var candidates []*cacheNode
	queryOverlap(idx.root, lo, hi, &candidates)
	var victims []*cacheEntry
	for _, node := range candidates {
		e := node.entry
		if e.required.Intersect(reasons) == 0 {
			continue
		}
		if !timeIntervalsOverlap(e.timeLo, e.timeHi, tlo, thi) {
			continue
		}
		victims = append(victims, e)
	}
	for _, e := range victims {
		idx.root = treapErase(idx.root, e)
		delete(idx.byKey, e.key)
		idx.count--
	}
	// Bump even when no live entry matched so an in-flight miss cannot
	// publish a result computed before this manifest arrived.
	c.generation[issuer]++
	c.stats.Invalidation++
	if len(victims) > 0 {
		c.stats.Invalidated += uint64(len(victims))
		c.recountLocked()
	}
}

func (c *Cache) recountLocked() {
	var total uint64
	for _, idx := range c.issuers {
		total += uint64(idx.count)
	}
	c.stats.Entries = total
}

// Stats returns a snapshot of cache counters.
func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Len returns the number of live cache entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, idx := range c.issuers {
		n += idx.count
	}
	return n
}

func treapPriority(seq uint64) uint64 {
	z := seq + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func entryLess(a, b *cacheEntry) bool {
	if c := a.serialLo.Cmp(b.serialLo); c != 0 {
		return c < 0
	}
	if c := a.serialHi.Cmp(b.serialHi); c != 0 {
		return c < 0
	}
	return a.seq < b.seq
}

func treapInsert(root *cacheNode, e *cacheEntry) *cacheNode {
	if root == nil {
		return &cacheNode{entry: e, priority: treapPriority(e.seq), maxHi: e.serialHi}
	}
	if entryLess(e, root.entry) {
		root.left = treapInsert(root.left, e)
		if root.left.priority < root.priority {
			root = rotateRight(root)
		}
	} else {
		root.right = treapInsert(root.right, e)
		if root.right.priority < root.priority {
			root = rotateLeft(root)
		}
	}
	updateMax(root)
	return root
}

func treapErase(root *cacheNode, e *cacheEntry) *cacheNode {
	if root == nil {
		return nil
	}
	if root.entry == e {
		return mergeTreap(root.left, root.right)
	}
	if entryLess(e, root.entry) {
		root.left = treapErase(root.left, e)
	} else {
		root.right = treapErase(root.right, e)
	}
	updateMax(root)
	return root
}

func mergeTreap(a, b *cacheNode) *cacheNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.priority < b.priority {
		a.right = mergeTreap(a.right, b)
		updateMax(a)
		return a
	}
	b.left = mergeTreap(a, b.left)
	updateMax(b)
	return b
}

func rotateRight(n *cacheNode) *cacheNode {
	l := n.left
	n.left = l.right
	l.right = n
	updateMax(n)
	updateMax(l)
	return l
}

func rotateLeft(n *cacheNode) *cacheNode {
	r := n.right
	n.right = r.left
	r.left = n
	updateMax(n)
	updateMax(r)
	return r
}

func updateMax(n *cacheNode) {
	if n == nil {
		return
	}
	n.maxHi = n.entry.serialHi
	if n.left != nil && n.left.maxHi.Cmp(n.maxHi) > 0 {
		n.maxHi = n.left.maxHi
	}
	if n.right != nil && n.right.maxHi.Cmp(n.maxHi) > 0 {
		n.maxHi = n.right.maxHi
	}
}

func queryOverlap(n *cacheNode, lo, hi Serial, out *[]*cacheNode) {
	if n == nil || n.maxHi.Cmp(lo) <= 0 {
		return
	}
	queryOverlap(n.left, lo, hi, out)
	if n.entry.serialLo.Cmp(hi) < 0 && lo.Cmp(n.entry.serialHi) < 0 {
		*out = append(*out, n)
	}
	if n.entry.serialLo.Cmp(hi) < 0 {
		queryOverlap(n.right, lo, hi, out)
	}
}

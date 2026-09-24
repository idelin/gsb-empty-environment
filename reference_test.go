package tenantcache

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// refFairShares is an independent water-filling implementation (sorted-level
// based) used to cross-check the engine's fairShares.
func refFairShares(pool int64, demands map[string]int64) map[string]int64 {
	type kv struct {
		id string
		d  int64
	}
	var pos []kv
	for id, d := range demands {
		if d > 0 {
			pos = append(pos, kv{id, d})
		}
	}
	sort.Slice(pos, func(i, j int) bool {
		if pos[i].d != pos[j].d {
			return pos[i].d < pos[j].d
		}
		return pos[i].id < pos[j].id
	})
	remaining := pool
	var level int64
	i := 0
	for i < len(pos) && remaining > 0 {
		n := int64(len(pos) - i)
		delta := pos[i].d - level
		if delta*n <= remaining {
			remaining -= delta * n
			level = pos[i].d
			for i < len(pos) && pos[i].d == level {
				i++
			}
		} else {
			level += remaining / n
			remaining = 0
		}
	}
	res := make(map[string]int64, len(demands))
	for id, d := range demands {
		if d < level {
			res[id] = d
		} else {
			res[id] = level
		}
	}
	return res
}

func TestFairSharesMatchReference(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	for iter := 0; iter < 5000; iter++ {
		n := 1 + rnd.Intn(6)
		demands := map[string]int64{}
		for j := 0; j < n; j++ {
			demands[string(rune('A'+j))] = int64(rnd.Intn(60))
		}
		pool := int64(rnd.Intn(150))
		got := fairShares(pool, demands)
		want := refFairShares(pool, demands)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pool=%d demands=%v: engine=%v reference=%v", pool, demands, got, want)
		}
		var sum int64
		for _, v := range got {
			sum += v
		}
		if sum > pool {
			t.Fatalf("fair shares %v exceed pool %d", got, pool)
		}
	}
}

// refEntry mirrors an entry for the independent reference model.
type refEntry struct {
	tenant     string
	key        string
	size       int64
	guaranteed bool
}

// refPrefix is one legal eviction prefix of a victim's entries.
type refPrefix struct {
	length int
	sum    int64
}

// refVictim is a tenant whose borrowed usage exceeds its fair share.
type refVictim struct {
	tenant  string
	excess  int64
	entries []refEntry // borrowed, unleased, not loading, in eviction order
}

// legalPrefixes returns the prefixes the stop rule allows: k entries may be
// taken iff k == 0 or the first k-1 entries sum to less than the excess.
func (v refVictim) legalPrefixes() []refPrefix {
	out := []refPrefix{{}}
	var acc int64
	for i, ent := range v.entries {
		if i > 0 && acc >= v.excess {
			break
		}
		acc += ent.size
		out = append(out, refPrefix{length: i + 1, sum: acc})
	}
	return out
}

func (v refVictim) maxPrefix() refPrefix {
	ps := v.legalPrefixes()
	return ps[len(ps)-1]
}

// refModel is an independently derived snapshot of the eviction-relevant state.
type refModel struct {
	free      int64
	ownAll    []refEntry // requester's evictable entries, borrowed-first LRU order
	reqVictim *refVictim // requester as fair-share victim, nil when within share
	victims   []refVictim
	fair      map[string]int64
}

// buildRefModel derives the reference model from engine internals using only
// the documented rules, re-implemented here independently.
func buildRefModel(e *Engine, reqID string, size int64) refModel {
	t := e.tenants[reqID]
	m := refModel{free: e.cap - e.totalUsed(), fair: map[string]int64{}}

	var gUsed int64
	for _, ent := range t.entries {
		if !ent.loading && ent.kind == kindGuaranteed {
			gUsed += ent.size
		}
	}
	borrowedClass := gUsed+size > t.guarantee

	demands := map[string]int64{}
	for _, o := range e.tenants {
		var b int64
		for _, ent := range o.entries {
			if !ent.loading && ent.kind == kindBorrowed {
				b += ent.size
			}
		}
		if b > 0 {
			demands[o.id] = b
		}
	}
	if borrowedClass {
		demands[reqID] += size
	}
	pool := e.cap
	for _, o := range e.tenants {
		pool -= o.guarantee // IdleAfter is disabled in these scenarios
	}
	m.fair = refFairShares(pool, demands)

	order := func(list []*entry) []refEntry {
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if a.kind != b.kind {
				return a.kind == kindBorrowed
			}
			if !a.lastAccess.Equal(b.lastAccess) {
				return a.lastAccess.Before(b.lastAccess)
			}
			return a.seq < b.seq
		})
		var out []refEntry
		for _, ent := range list {
			out = append(out, refEntry{
				tenant: ent.tenant.id, key: ent.key, size: ent.size,
				guaranteed: ent.kind == kindGuaranteed,
			})
		}
		return out
	}

	var own []*entry
	for _, ent := range t.entries {
		if !ent.loading && ent.leases == 0 {
			own = append(own, ent)
		}
	}
	m.ownAll = order(own)

	for _, o := range e.tenants {
		var b int64
		var cand []*entry
		for _, ent := range o.entries {
			if !ent.loading && ent.kind == kindBorrowed {
				b += ent.size
				if ent.leases == 0 {
					cand = append(cand, ent)
				}
			}
		}
		if excess := b - m.fair[o.id]; excess > 0 {
			v := refVictim{tenant: o.id, excess: excess, entries: order(cand)}
			if o.id == reqID {
				m.reqVictim = &v
			} else {
				m.victims = append(m.victims, v)
			}
		}
	}
	sort.Slice(m.victims, func(i, j int) bool {
		if m.victims[i].excess != m.victims[j].excess {
			return m.victims[i].excess > m.victims[j].excess
		}
		return m.victims[i].tenant < m.victims[j].tenant
	})
	return m
}

// feasibleByEnumeration exhaustively enumerates the engine-reachable eviction
// combinations — fair-share victim prefixes first (the requester's own excess
// is a prefix of its borrowed entries), then the requester's remaining own
// entries — and reports whether any combination frees >= want bytes.
func (m refModel) feasibleByEnumeration(want int64) bool {
	if want <= 0 {
		return true
	}
	reqPrefixes := []refPrefix{{}}
	if m.reqVictim != nil {
		reqPrefixes = m.reqVictim.legalPrefixes()
	}
	ownSums := make([]int64, len(m.ownAll)+1)
	for i, ent := range m.ownAll {
		ownSums[i+1] = ownSums[i] + ent.size
	}
	victimPrefixes := make([][]refPrefix, len(m.victims))
	for i, v := range m.victims {
		victimPrefixes[i] = v.legalPrefixes()
	}
	var dfs func(v int, acc int64) bool
	dfs = func(v int, acc int64) bool {
		if acc >= want {
			return true
		}
		if v == len(victimPrefixes) {
			return false
		}
		for _, p := range victimPrefixes[v] {
			if dfs(v+1, acc+p.sum) {
				return true
			}
		}
		return false
	}
	for _, rp := range reqPrefixes {
		// after the requester's victim prefix of rp.length borrowed entries,
		// its remaining own entries extend from there to the end of ownAll
		for j := rp.length; j <= len(m.ownAll); j++ {
			if dfs(0, rp.sum+ownSums[j]-ownSums[rp.length]) {
				return true
			}
		}
	}
	return false
}

// TestAllocationMatchesEnumerationReference verifies, over randomized small
// scenarios, that the engine grants an allocation exactly when exhaustive
// enumeration of the legal eviction choices finds room, and that the
// engine's actual evictions are legal and order-minimal.
func TestAllocationMatchesEnumerationReference(t *testing.T) {
	ids := []string{"A", "B", "C"}
	for seed := int64(0); seed < 400; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		clk := newFakeClock()
		e, err := NewEngine(Config{Capacity: 100, Clock: clk.now})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := e.AddTenant(id, "", int64(rnd.Intn(30))); err != nil {
				t.Fatal(err)
			}
		}
		type kref struct{ tenant, key string }
		var keys []kref
		var held []*Lease
		for i := 0; i < 4+rnd.Intn(8); i++ {
			tn := ids[rnd.Intn(len(ids))]
			key := fmt.Sprintf("%s-%d", tn, i)
			if _, err := e.Put(tn, key, 5+int64(rnd.Intn(11))); err == nil {
				keys = append(keys, kref{tn, key})
			}
		}
		for i := 0; i < rnd.Intn(3) && len(keys) > 0; i++ {
			k := keys[rnd.Intn(len(keys))]
			if l, _, err := e.Get(k.tenant, k.key); err == nil {
				held = append(held, l)
			}
		}
		reqT := ids[rnd.Intn(len(ids))]
		reqSize := int64(10 + rnd.Intn(31))

		model := buildRefModel(e, reqT, reqSize)
		want := model.feasibleByEnumeration(reqSize - model.free)

		cert, err := e.Put(reqT, "REQUEST", reqSize)
		granted := err == nil
		if granted != want {
			t.Fatalf("seed=%d: engine granted=%v, enumeration says %v\ncert=%s", seed, granted, want, cert)
		}
		if !granted {
			if cert.Unsat == nil {
				t.Fatalf("seed=%d: rejected allocation must carry an unsat reason", seed)
			}
			continue
		}
		if cert.FairShares != nil && !reflect.DeepEqual(cert.FairShares, model.fair) {
			t.Fatalf("seed=%d: cert fair shares %v != reference %v", seed, cert.FairShares, model.fair)
		}
		need := reqSize - model.free
		if need <= 0 {
			if len(cert.Evicted) != 0 {
				t.Fatalf("seed=%d: evictions despite sufficient free space", seed)
			}
			continue
		}
		var sum int64
		byTenant := map[string][]string{}
		for _, ev := range cert.Evicted {
			sum += ev.Size
			byTenant[ev.Tenant] = append(byTenant[ev.Tenant], ev.Key)
		}
		if sum < need {
			t.Fatalf("seed=%d: evicted %d bytes but needed %d", seed, sum, need)
		}
		if last := cert.Evicted[len(cert.Evicted)-1]; sum-last.Size >= need {
			t.Fatalf("seed=%d: eviction set not order-minimal (last=%v)", seed, last)
		}
		// requester's evicted keys must be a prefix of its own eviction order
		ks := byTenant[reqT]
		if len(ks) > len(model.ownAll) {
			t.Fatalf("seed=%d: too many own evictions", seed)
		}
		numBorrowed := 0
		for i, k := range ks {
			if model.ownAll[i].key != k {
				t.Fatalf("seed=%d: own eviction %q breaks deterministic order", seed, k)
			}
			if !model.ownAll[i].guaranteed {
				numBorrowed = i + 1
			}
		}
		// pass 2 (own entries beyond the requester's victim prefix) may only
		// run after every victim yielded its stop-rule maximum
		reqMax := 0
		if model.reqVictim != nil {
			reqMax = model.reqVictim.maxPrefix().length
		}
		if len(ks) > reqMax {
			for _, v := range model.victims {
				if got := len(byTenant[v.tenant]); got != v.maxPrefix().length {
					t.Fatalf("seed=%d: own entries evicted before victim %s yielded max (%d != %d)",
						seed, v.tenant, got, v.maxPrefix().length)
				}
			}
			if numBorrowed < reqMax {
				t.Fatalf("seed=%d: requester borrowed evictions below its fair-share excess", seed)
			}
		}
		// other tenants: evictions must be a stop-rule-respecting prefix of
		// their borrowed entries, and only from tenants over fair share
		for tn, evicted := range byTenant {
			if tn == reqT {
				continue
			}
			var v *refVictim
			for i := range model.victims {
				if model.victims[i].tenant == tn {
					v = &model.victims[i]
				}
			}
			if v == nil {
				t.Fatalf("seed=%d: tenant %s evicted despite being within fair share", seed, tn)
			}
			if len(evicted) > len(v.entries) {
				t.Fatalf("seed=%d: too many evictions from %s", seed, tn)
			}
			var acc int64
			for i, k := range evicted {
				if v.entries[i].key != k {
					t.Fatalf("seed=%d: eviction %q from %s breaks deterministic order", seed, k, tn)
				}
				if i > 0 && acc >= v.excess {
					t.Fatalf("seed=%d: eviction beyond fair-share excess from %s", seed, tn)
				}
				acc += v.entries[i].size
			}
		}
		for _, l := range held {
			l.Release()
		}
	}
}

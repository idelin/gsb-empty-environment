package main

import (
	"container/heap"
	"fmt"
	"net/netip"
)

type timerItem struct {
	at      int64
	host    string
	entryID int
}

type timerHeap []timerItem

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].host < h[j].host
}
func (h timerHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *timerHeap) Push(x interface{}) { *h = append(*h, x.(timerItem)) }
func (h *timerHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// Engine is a deterministic, offline replay of the DNS cache, SSRF
// validation, dial and connection-pool lifecycle.
type Engine struct {
	scenario  Scenario
	prefixes  []netip.Prefix
	seq       int
	events    []Event
	vc        map[string]int
	cache     map[string]*cacheEntry
	timers    *timerHeap
	pools     map[string][]*conn
	connCount int
	resolveN  map[string]int
}

func newEngine(sc Scenario) (*Engine, error) {
	prefixes := make([]netip.Prefix, 0, len(sc.Forbidden))
	for _, cidr := range sc.Forbidden {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid forbidden cidr %q: %w", cidr, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	for name, host := range sc.Hosts {
		if host.Port == 0 || host.TTL <= 0 || len(host.Steps) == 0 {
			return nil, fmt.Errorf("host %q requires port, positive ttl and at least one step", name)
		}
		for i, step := range host.Steps {
			if len(step) == 0 {
				return nil, fmt.Errorf("host %q resolution step %d is empty", name, i)
			}
		}
	}
	seenReq := map[string]bool{}
	for _, req := range sc.Requests {
		if req.ID == "" {
			return nil, fmt.Errorf("request at host %q is missing id", req.Host)
		}
		if seenReq[req.ID] {
			return nil, fmt.Errorf("duplicate request id %q", req.ID)
		}
		seenReq[req.ID] = true
	}
	return &Engine{
		scenario: sc,
		prefixes: prefixes,
		vc:       map[string]int{},
		events:   []Event{},
		cache:    map[string]*cacheEntry{},
		timers:   &timerHeap{},
		pools:    map[string][]*conn{},
		resolveN: map[string]int{},
	}, nil
}

func (e *Engine) isForbidden(addr string) (bool, error) {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false, fmt.Errorf("invalid resolved address %q: %w", addr, err)
	}
	ip = ip.Unmap()
	for _, prefix := range e.prefixes {
		if prefix.Contains(ip) {
			return true, nil
		}
	}
	return false, nil
}

func mergeVC(dst map[string]int, parents ...map[string]int) map[string]int {
	out := make(map[string]int, len(dst))
	for k, v := range dst {
		out[k] = v
	}
	for _, p := range parents {
		for k, v := range p {
			if v > out[k] {
				out[k] = v
			}
		}
	}
	return out
}

func (e *Engine) emit(evType string, at int64, reqID, host string, parents []int, detail map[string]string, parentVCs ...map[string]int) Event {
	e.seq++
	clock := mergeVC(e.vc, parentVCs...)
	clock[evType]++
	e.vc = clock
	vcCopy := make(map[string]int, len(clock))
	for k, v := range clock {
		vcCopy[k] = v
	}
	var parentCopy []int
	if len(parents) > 0 {
		parentCopy = append(parentCopy, parents...)
	}
	var detailCopy map[string]string
	if len(detail) > 0 {
		detailCopy = make(map[string]string, len(detail))
		for k, v := range detail {
			detailCopy[k] = v
		}
	}
	ev := Event{
		Seq:       e.seq,
		Type:      evType,
		At:        at,
		RequestID: reqID,
		Host:      host,
		VC:        vcCopy,
		Parents:   parentCopy,
		Detail:    detailCopy,
	}
	e.events = append(e.events, ev)
	return ev
}

// advanceTo fires every due TTL timer at or before t. Expiry is inclusive:
// an entry is dead at stored+ttl, so a lookup on the boundary misses.
func (e *Engine) advanceTo(t int64) {
	for e.timers.Len() > 0 && (*e.timers)[0].at <= t {
		item := heap.Pop(e.timers).(timerItem)
		cached, ok := e.cache[item.host]
		if !ok || cached.seq != item.entryID {
			continue
		}
		delete(e.cache, item.host)
		e.emit(EvTimerExpiry, item.at, "", item.host, []int{cached.seq}, map[string]string{
			"expires_at": fmt.Sprintf("%d", item.at),
			"ttl":        fmt.Sprintf("%d", cached.ttl),
		}, e.events[cached.seq-1].VC)
	}
}

// resolveAt returns the addresses visible to a lookup at time t plus the
// lookup/resolve events that produced them.
func (e *Engine) resolveAt(reqID, host string, t int64) ([]string, []Event, error) {
	e.advanceTo(t)
	var parents []int
	if len(e.events) > 0 {
		parents = []int{e.events[len(e.events)-1].Seq}
	}
	lookup := e.emit(EvCacheLookup, t, reqID, host, parents, map[string]string{
		"virtual_time": fmt.Sprintf("%d", t),
	})

	if cached, ok := e.cache[host]; ok {
		e.emit(EvCacheStore, t, reqID, host, []int{lookup.Seq}, map[string]string{
			"source":     "cache_hit",
			"addresses":  joinAddrs(cached.addrs),
			"stored_at":  fmt.Sprintf("%d", cached.stored),
			"expires_at": fmt.Sprintf("%d", cached.expires),
			"ttl":        fmt.Sprintf("%d", cached.ttl),
		}, lookup.VC)
		return append([]string(nil), cached.addrs...), []Event{lookup}, nil
	}

	hostCfg, ok := e.scenario.Hosts[host]
	if !ok {
		return nil, nil, fmt.Errorf("request %q references unknown host %q", reqID, host)
	}
	idx := e.resolveN[host]
	if idx >= len(hostCfg.Steps) {
		return nil, nil, fmt.Errorf("host %q resolution script exhausted at request %q", host, reqID)
	}
	e.resolveN[host] = idx + 1
	addrs := append([]string(nil), hostCfg.Steps[idx]...)

	resolve := e.emit(EvResolve, t, reqID, host, []int{lookup.Seq}, map[string]string{
		"step":      fmt.Sprintf("%d", idx),
		"addresses": joinAddrs(addrs),
		"ttl":       fmt.Sprintf("%d", hostCfg.TTL),
	}, lookup.VC)

	for _, addr := range addrs {
		if _, err := e.isForbidden(addr); err != nil {
			return nil, nil, err
		}
	}

	entry := &cacheEntry{
		addrs:   addrs,
		stored:  t,
		expires: t + int64(hostCfg.TTL),
		ttl:     hostCfg.TTL,
		seq:     resolve.Seq,
	}
	e.cache[host] = entry
	e.emit(EvCacheStore, t, reqID, host, []int{resolve.Seq}, map[string]string{
		"source":     "fresh",
		"addresses":  joinAddrs(addrs),
		"stored_at":  fmt.Sprintf("%d", t),
		"expires_at": fmt.Sprintf("%d", entry.expires),
		"ttl":        fmt.Sprintf("%d", hostCfg.TTL),
	}, resolve.VC)
	heap.Push(e.timers, timerItem{at: entry.expires, host: host, entryID: resolve.Seq})

	return addrs, []Event{lookup, resolve}, nil
}

func peerIP(peer string) string {
	addr, err := netip.ParseAddrPort(peer)
	if err != nil {
		return peer
	}
	return addr.Addr().Unmap().String()
}

func joinAddrs(addrs []string) string {
	out := ""
	for i, addr := range addrs {
		if i > 0 {
			out += ","
		}
		out += addr
	}
	return out
}

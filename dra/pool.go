package dra

import (
	"sort"
	"sync"
)

// poolKey identifies a pool within a node.
type poolKey struct {
	driver string
	pool   string
}

// poolState aggregates every slice observed for one pool.
type poolState struct {
	nodeName           string
	driver             string
	poolName           string
	highestGeneration  int64
	resourceSliceCount int64
	slices             map[string]struct{}
	devices            map[string]Device
}

// poolSnapshot is the immutable materialized view handed to the solver.
type poolSnapshot struct {
	nodeName   string
	driver     string
	poolName   string
	generation int64
	complete   bool
	sliceCount int64
	observed   int
	devices    []deviceView
}

// nodeView stores the per-node pool registry.
type nodeView struct {
	mu    sync.RWMutex
	pools map[poolKey]*poolState
}

func newNodeView() *nodeView {
	return &nodeView{pools: map[poolKey]*poolState{}}
}

// applySlice ingests one ResourceSlice. A newer generation resets all state
// observed for the previous generation so stale slices never leak through.
func (n *nodeView) applySlice(slice ResourceSlice) {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := poolKey{driver: slice.Driver, pool: slice.Pool.Name}
	state, ok := n.pools[key]
	if !ok {
		state = &poolState{
			nodeName: slice.NodeName,
			driver:   slice.Driver,
			poolName: slice.Pool.Name,
			slices:   map[string]struct{}{},
			devices:  map[string]Device{},
		}
		n.pools[key] = state
	}
	if slice.Pool.Generation > state.highestGeneration {
		state.highestGeneration = slice.Pool.Generation
		state.resourceSliceCount = slice.Pool.ResourceSliceCount
		state.slices = map[string]struct{}{}
		state.devices = map[string]Device{}
	} else if slice.Pool.ResourceSliceCount > state.resourceSliceCount {
		state.resourceSliceCount = slice.Pool.ResourceSliceCount
	}
	state.slices[slice.Name] = struct{}{}
	for _, dev := range slice.Devices {
		state.devices[dev.Name] = dev
	}
}

// removeSlice drops a slice (and its devices). It is used when a driver
// deletes a published slice.
func (n *nodeView) removeSlice(slice ResourceSlice) {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := poolKey{driver: slice.Driver, pool: slice.Pool.Name}
	state, ok := n.pools[key]
	if !ok {
		return
	}
	if _, ok := state.slices[slice.Name]; !ok {
		return
	}
	delete(state.slices, slice.Name)
	for _, dev := range slice.Devices {
		delete(state.devices, dev.Name)
	}
}

// PoolStatus describes the materialized state of one pool.
type PoolStatus struct {
	NodeName           string
	Driver             string
	Pool               string
	Generation         int64
	ResourceSliceCount int64
	ObservedSlices     int
	Complete           bool
	DeviceCount        int
}

// snapshotPools materializes pools for the node, marking every pool whose
// current generation is not fully reported as Complete=false. Incomplete
// pools are still returned so callers can observe why they were ignored, but
// the solver never allocates from them.
func (n *nodeView) snapshotPools() []poolSnapshot {
	n.mu.RLock()
	defer n.mu.RUnlock()

	keys := make([]poolKey, 0, len(n.pools))
	for key := range n.pools {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].driver != keys[j].driver {
			return keys[i].driver < keys[j].driver
		}
		return keys[i].pool < keys[j].pool
	})

	out := make([]poolSnapshot, 0, len(keys))
	for _, key := range keys {
		state := n.pools[key]
		complete := int64(len(state.slices)) >= state.resourceSliceCount
		names := make([]string, 0, len(state.devices))
		for name := range state.devices {
			names = append(names, name)
		}
		sort.Strings(names)
		views := make([]deviceView, 0, len(names))
		for _, name := range names {
			views = append(views, deviceView{
				driver: state.driver,
				pool:   state.poolName,
				device: state.devices[name],
			})
		}
		out = append(out, poolSnapshot{
			nodeName:   state.nodeName,
			driver:     state.driver,
			poolName:   state.poolName,
			generation: state.highestGeneration,
			complete:   complete,
			sliceCount: state.resourceSliceCount,
			observed:   len(state.slices),
			devices:    views,
		})
	}
	return out
}

// poolStatuses returns an observability view of every known pool.
func (n *nodeView) poolStatuses() []PoolStatus {
	snapshots := n.snapshotPools()
	out := make([]PoolStatus, 0, len(snapshots))
	for _, snap := range snapshots {
		out = append(out, PoolStatus{
			NodeName:           snap.nodeName,
			Driver:             snap.driver,
			Pool:               snap.poolName,
			Generation:         snap.generation,
			ResourceSliceCount: snap.sliceCount,
			ObservedSlices:     snap.observed,
			Complete:           snap.complete,
			DeviceCount:        len(snap.devices),
		})
	}
	return out
}

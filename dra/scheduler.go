package dra

import (
	"errors"
	"fmt"
	"sync"
)

// Scheduler is the importable entry point. It owns the cluster-wide view of
// published ResourceSlices, DeviceClasses and ResourceClaims, and executes
// filter/allocation plus reservation transactions for consumers (pods).
type Scheduler struct {
	mu sync.RWMutex

	nodes   map[string]*nodeView
	classes map[string]*DeviceClass
	claims  map[string]*ResourceClaim

	// occupied holds the (Driver,Pool,Device) keys already allocated on a
	// node, so two claims can never take the same physical device.
	occupied map[string]map[string]struct{}
}

// NewScheduler creates an empty scheduler.
func NewScheduler() *Scheduler {
	return &Scheduler{
		nodes:    map[string]*nodeView{},
		classes:  map[string]*DeviceClass{},
		claims:   map[string]*ResourceClaim{},
		occupied: map[string]map[string]struct{}{},
	}
}

// AddClass registers or replaces a DeviceClass.
func (s *Scheduler) AddClass(class DeviceClass) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := class
	s.classes[class.Name] = &cp
}

// AddClaim registers a ResourceClaim.
func (s *Scheduler) AddClaim(claim ResourceClaim) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := claim
	s.claims[claim.Key()] = &cp
}

// GetClaim returns a defensive copy of the named claim.
func (s *Scheduler) GetClaim(namespace, name string) (ResourceClaim, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	claim, ok := s.claims[ClaimKey(namespace, name)]
	if !ok {
		return ResourceClaim{}, false
	}
	return *claim, true
}

// ApplySlice ingests one published ResourceSlice into its node view.
func (s *Scheduler) ApplySlice(slice ResourceSlice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.nodes[slice.NodeName]
	if !ok {
		node = newNodeView()
		s.nodes[slice.NodeName] = node
	}
	node.applySlice(slice)
}

// RemoveSlice handles deletion of a published ResourceSlice.
func (s *Scheduler) RemoveSlice(slice ResourceSlice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.nodes[slice.NodeName]
	if ok {
		node.removeSlice(slice)
	}
}

// PoolStatuses exposes the materialized pool view of a node.
func (s *Scheduler) PoolStatuses(nodeName string) []PoolStatus {
	s.mu.RLock()
	node, ok := s.nodes[nodeName]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	return node.poolStatuses()
}

// EnsurePoolsComplete returns ErrIncompletePool naming the first pool of a
// node whose current generation has not received all of its slices. The
// filter/allocation path itself silently ignores such pools; this helper lets
// callers distinguish "pool topology still reporting" from a real no-fit.
func (s *Scheduler) EnsurePoolsComplete(nodeName string) error {
	s.mu.RLock()
	node, ok := s.nodes[nodeName]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: node %q has no resource slices", ErrNotFound, nodeName)
	}
	for _, status := range node.poolStatuses() {
		if !status.Complete {
			return fmt.Errorf("%w: node %q driver %q pool %q generation %d has %d/%d slices",
				ErrIncompletePool, nodeName, status.Driver, status.Pool,
				status.Generation, status.ObservedSlices, status.ResourceSliceCount)
		}
	}
	return nil
}

// Filter reports whether the claim can be allocated on the node. It never
// mutates scheduler state: incomplete pools are hidden and occupied devices
// are excluded, so the result matches what Reserve would attempt.
func (s *Scheduler) Filter(nodeName string, claim *ResourceClaim) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.solveForNode(nodeName, claim)
	if err != nil {
		if isNoFit(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Reserve implements the scheduler Reserve phase for one consumer:
//   - an already allocated claim reuses its Allocation (shared reference
//     counting; no hardware is deducted again) and must stay on its node;
//   - an unallocated claim runs the backtracking solver, marks every
//     selected device occupied and persists the resulting Allocation;
//   - non-shared claims accept a single consumer, shared claims many.
//
// The whole operation is transactional: on any failure no state changes.
func (s *Scheduler) Reserve(nodeName string, claim *ResourceClaim, consumer Consumer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, ok := s.claims[claim.Key()]
	if !ok {
		cp := *claim
		stored = &cp
		s.claims[claim.Key()] = stored
	}

	if stored.Allocation != nil {
		if stored.Allocation.Node != nodeName {
			return fmt.Errorf("%w: claim %s is on node %q, requested %q",
				ErrNodeMismatch, stored.Key(), stored.Allocation.Node, nodeName)
		}
		return s.addConsumer(stored, consumer)
	}

	allocation, err := s.solveForNode(nodeName, stored)
	if err != nil {
		return err
	}

	busy := s.occupied[nodeName]
	if busy == nil {
		busy = map[string]struct{}{}
		s.occupied[nodeName] = busy
	}
	for _, req := range allocation.Results {
		for _, dev := range req.Devices {
			busy[dev.Key()] = struct{}{}
		}
	}
	stored.Allocation = allocation
	return s.addConsumer(stored, consumer)
}

// Unreserve removes one consumer from a claim. An exclusive claim releases
// its hardware immediately; a shared claim keeps the allocation while any
// consumer remains and releases only when the last consumer goes away.
func (s *Scheduler) Unreserve(claim *ResourceClaim, consumer Consumer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, ok := s.claims[claim.Key()]
	if !ok {
		return fmt.Errorf("%w: claim %s", ErrNotFound, claim.Key())
	}

	idx := -1
	for i, entry := range stored.ReservedFor {
		if entry.Key() == consumer.Key() {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: consumer %s on claim %s",
			ErrNotReserved, consumer.Key(), stored.Key())
	}
	stored.ReservedFor = append(stored.ReservedFor[:idx], stored.ReservedFor[idx+1:]...)

	if !stored.Shared || len(stored.ReservedFor) == 0 {
		s.releaseAllocation(stored)
	}
	return nil
}

// DeleteClaim removes a claim outright and always releases its hardware.
func (s *Scheduler) DeleteClaim(namespace, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := ClaimKey(namespace, name)
	stored, ok := s.claims[key]
	if !ok {
		return fmt.Errorf("%w: claim %s", ErrNotFound, key)
	}
	s.releaseAllocation(stored)
	delete(s.claims, key)
	return nil
}

func (s *Scheduler) addConsumer(stored *ResourceClaim, consumer Consumer) error {
	for _, entry := range stored.ReservedFor {
		if entry.Key() == consumer.Key() {
			return nil
		}
	}
	if !stored.Shared && len(stored.ReservedFor) > 0 {
		return fmt.Errorf("%w: claim %s is held by %s",
			ErrAlreadyReserved, stored.Key(), stored.ReservedFor[0].Key())
	}
	stored.ReservedFor = append(stored.ReservedFor, consumer)
	return nil
}

func (s *Scheduler) releaseAllocation(stored *ResourceClaim) {
	if stored.Allocation == nil {
		stored.ReservedFor = nil
		return
	}
	busy := s.occupied[stored.Allocation.Node]
	for _, req := range stored.Allocation.Results {
		for _, dev := range req.Devices {
			delete(busy, dev.Key())
		}
	}
	if len(busy) == 0 {
		delete(s.occupied, stored.Allocation.Node)
	}
	stored.Allocation = nil
	stored.ReservedFor = nil
}

func (s *Scheduler) solveForNode(nodeName string, claim *ResourceClaim) (*AllocationResult, error) {
	node, ok := s.nodes[nodeName]
	if !ok {
		return nil, fmt.Errorf("%w: node %q has no resource slices", ErrNotFound, nodeName)
	}
	snapshots := node.snapshotPools()
	complete := make([]poolSnapshot, 0, len(snapshots))
	for _, snap := range snapshots {
		if snap.complete {
			complete = append(complete, snap)
		}
	}

	occupied := map[string]struct{}{}
	for key := range s.occupied[nodeName] {
		occupied[key] = struct{}{}
	}

	runner := &solver{
		nodeName: nodeName,
		classes:  s.classes,
		pools:    complete,
		occupied: occupied,
		claim:    claim,
	}
	return runner.solve()
}

func isNoFit(err error) bool {
	return errors.Is(err, ErrNoDevices) || errors.Is(err, ErrIncompletePool)
}

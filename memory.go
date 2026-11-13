package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrStalePage is returned when a PageRef generation no longer matches.
	ErrStalePage = errors.New("netsec: stale page reference")
	// ErrCOWFailed is returned when copy-on-write allocation was injected to fail.
	ErrCOWFailed = errors.New("netsec: copy-on-write failed")
)

// Config controls a Memory host.
type Config struct {
	// Policy may be a MergePolicy implementation or a callback with the shape
	// func(Page, Page) bool (or func(*Page, *Page) bool).
	Policy     any
	PolicyFunc func(incoming, resident Page) bool
	Costs      CostModel
	DigestFunc DigestFunc

	// COWFault is consulted before every copy-on-write allocation.  Returning
	// an error makes that COW fail without changing refcounts or contents.
	COWFault func(Page) error
}

type virtualPage struct {
	id         PageID
	generation Generation

	domain    DomainID
	digest    Digest
	tags      TagSet
	shareable bool

	physical PhysicalPageID
	content  []byte
}

type physicalPage struct {
	id         PhysicalPageID
	generation Generation
	digest     Digest
	content    []byte
	refs       int
	members    map[PageID]struct{}
	cowCount   uint64
}

type mapSlot struct {
	generation Generation
	page       *virtualPage
	inUse      bool
}

type physicalSlot struct {
	generation Generation
	page       *physicalPage
	inUse      bool
}

// Memory is a deterministic, mutex-protected isolation host memory model.
type Memory struct {
	mu sync.Mutex

	policy   MergePolicy
	costs    CostModel
	digest   DigestFunc
	cowFault func(Page) error

	mapSlots  []mapSlot
	freeMaps  []PageID
	nextMapID PageID

	physicalSlots []physicalSlot
	freePhysical  []PhysicalPageID
	nextPhysical  PhysicalPageID

	stats Stats

	failCOWOnce bool
	failCOWErr  error
}

// NewMemory constructs a host.  With no configuration it uses the
// conservative policy, exact default costs and SHA-256 digests.
func NewMemory(configs ...Config) *Memory {
	var cfg Config
	if len(configs) > 0 {
		cfg = configs[0]
	}
	policy := coercePolicy(cfg.Policy)
	if policy == nil && cfg.PolicyFunc != nil {
		policy = PolicyFunc(cfg.PolicyFunc)
	}
	if policy == nil {
		policy = ConservativePolicy{}
	}
	m := &Memory{
		policy:   policy,
		costs:    cfg.Costs.Normalize(),
		digest:   cfg.DigestFunc,
		cowFault: cfg.COWFault,
	}
	if m.digest == nil {
		m.digest = SHA256Digest
	}
	return m
}

// Host and DedupMemory are aliases for Memory.
type Host = Memory
type MemoryHost = Memory
type DedupMemory = Memory

// NewHost constructs a host with the same defaults as NewMemory.
func NewHost(configs ...Config) *Host { return NewMemory(configs...) }

// NewDedupMemory constructs a host with the same defaults as NewMemory.
func NewDedupMemory(configs ...Config) *DedupMemory { return NewMemory(configs...) }

// SetPolicy replaces the merge policy.  It accepts the same forms as
// Config.Policy.
func (m *Memory) SetPolicy(value any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	policy := coercePolicy(value)
	if policy == nil {
		policy = ConservativePolicy{}
	}
	m.policy = policy
}

// Policy returns the current merge policy.
func (m *Memory) Policy() MergePolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policy
}

// SetCosts replaces the cost model.
func (m *Memory) SetCosts(costs CostModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.costs = costs.Normalize()
}

// Costs returns the normalized cost model.
func (m *Memory) Costs() CostModel {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.costs
}

// SetDigestFunc replaces the digest function used for future pages.
func (m *Memory) SetDigestFunc(fn DigestFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fn == nil {
		fn = SHA256Digest
	}
	m.digest = fn
}

// SetCOWFault installs a persistent COW fault callback.
func (m *Memory) SetCOWFault(fn func(Page) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cowFault = fn
}

// FailNextCOW makes the next copy-on-write fail.  A nil error uses
// ErrCOWFailed.
func (m *Memory) FailNextCOW(err ...error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCOWOnce = true
	m.failCOWErr = ErrCOWFailed
	if len(err) > 0 && err[0] != nil {
		m.failCOWErr = err[0]
	}
}

// FailCOW is an alias for FailNextCOW.
func (m *Memory) FailCOW(err ...error) { m.FailNextCOW(err...) }

func (m *Memory) now() Stats {
	s := m.stats
	s.LivePages = 0
	s.LivePhysicalPages = 0
	s.SharedPhysicalPages = 0
	s.MergedPages = 0
	for _, slot := range m.mapSlots {
		if slot.inUse {
			s.LivePages++
		}
	}
	for _, slot := range m.physicalSlots {
		if slot.inUse && slot.page.refs > 0 {
			s.LivePhysicalPages++
			if slot.page.refs > 1 {
				s.SharedPhysicalPages++
				s.MergedPages += slot.page.refs - 1
			}
		}
	}
	return s
}

// Stats returns a counter snapshot.
func (m *Memory) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now()
}

// CreatePage installs a page and returns a generation-checked reference.
func (m *Memory) CreatePage(spec PageSpec) PageRef {
	ref, err := m.CreatePageE(spec)
	if err != nil {
		panic(err)
	}
	return ref
}

// NewPage is an alias for CreatePage.
func (m *Memory) NewPage(spec PageSpec) PageRef { return m.CreatePage(spec) }

// CreatePageE is CreatePage with an error return.
func (m *Memory) CreatePageE(spec PageSpec) (PageRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	content := append([]byte(nil), spec.Content...)
	digest := spec.Digest
	if digest == "" {
		digest = m.digest(content)
	}
	page := &virtualPage{
		domain:    spec.DomainValue(),
		digest:    digest,
		tags:      spec.TagValues(),
		shareable: spec.Shareable,
		content:   content,
	}
	m.installMapping(page)

	// Offer the page to every resident physical page with the same digest in
	// deterministic physical-slot order.  The first policy-approved candidate
	// wins.  A digest collision therefore merges different contents.
	for _, slot := range m.physicalSlots {
		if !slot.inUse || slot.page.refs == 0 || slot.page.digest != digest {
			continue
		}
		resident := m.snapshotPhysicalResident(slot.page)
		incoming := m.snapshotVirtual(page)
		if m.policy != nil && m.policy.CanMerge(incoming, resident) {
			m.attach(page, slot.page)
			m.stats.Merges++
			return PageRef{ID: page.id, Generation: page.generation}, nil
		}
	}

	physical := m.allocatePhysical(digest, content)
	page.physical = physical.id
	physical.refs = 1
	physical.members[page.id] = struct{}{}
	return PageRef{ID: page.id, Generation: page.generation}, nil
}

func (m *Memory) installMapping(page *virtualPage) {
	if len(m.freeMaps) > 0 {
		id := m.freeMaps[len(m.freeMaps)-1]
		m.freeMaps = m.freeMaps[:len(m.freeMaps)-1]
		slot := &m.mapSlots[id]
		slot.generation++
		slot.page = page
		slot.inUse = true
		page.id = id
		page.generation = slot.generation
		return
	}
	id := m.nextMapID
	m.nextMapID++
	m.mapSlots = append(m.mapSlots, mapSlot{
		generation: 1,
		page:       page,
		inUse:      true,
	})
	page.id = id
	page.generation = 1
}

func (m *Memory) allocatePhysical(digest Digest, content []byte) *physicalPage {
	page := &physicalPage{
		digest:  digest,
		content: append([]byte(nil), content...),
		members: make(map[PageID]struct{}),
	}
	if len(m.freePhysical) > 0 {
		id := m.freePhysical[len(m.freePhysical)-1]
		m.freePhysical = m.freePhysical[:len(m.freePhysical)-1]
		slot := &m.physicalSlots[id]
		slot.generation++
		slot.page = page
		slot.inUse = true
		page.id = id
		page.generation = slot.generation
		return page
	}
	id := m.nextPhysical
	m.nextPhysical++
	m.physicalSlots = append(m.physicalSlots, physicalSlot{
		generation: 1,
		page:       page,
		inUse:      true,
	})
	page.id = id
	page.generation = 1
	return page
}

func (m *Memory) attach(page *virtualPage, physical *physicalPage) {
	physical.refs++
	physical.members[page.id] = struct{}{}
	page.physical = physical.id
}

func (m *Memory) detach(page *virtualPage) (PhysicalPageID, Generation, bool) {
	physical := m.lookupPhysical(page.physical)
	if physical == nil {
		return 0, 0, false
	}
	delete(physical.members, page.id)
	physical.refs--
	id, generation := physical.id, physical.generation
	if physical.refs == 0 {
		m.reclaimPhysical(physical)
		return id, generation, true
	}
	return id, generation, false
}

func (m *Memory) reclaimPhysical(physical *physicalPage) {
	slot := &m.physicalSlots[physical.id]
	slot.page = nil
	slot.inUse = false
	m.freePhysical = append(m.freePhysical, physical.id)
	m.stats.ReclaimedPages++
}

func (m *Memory) lookupPhysical(id PhysicalPageID) *physicalPage {
	if int(id) >= len(m.physicalSlots) {
		return nil
	}
	slot := &m.physicalSlots[id]
	if !slot.inUse || slot.page == nil {
		return nil
	}
	return slot.page
}

func (m *Memory) resolve(ref PageRef) (*virtualPage, error) {
	if int(ref.ID) >= len(m.mapSlots) {
		return nil, ErrStalePage
	}
	slot := &m.mapSlots[ref.ID]
	if !slot.inUse || slot.page == nil || slot.page.generation != ref.Generation {
		return nil, ErrStalePage
	}
	return slot.page, nil
}

// Resolve checks that ref is live.
func (m *Memory) Resolve(ref PageRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.resolve(ref)
	return err
}

// Page returns a snapshot of the referenced virtual page.
func (m *Memory) Page(ref PageRef) (Page, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	page, err := m.resolve(ref)
	if err != nil {
		return Page{}, false
	}
	return m.snapshotVirtual(page), true
}

// Lookup is an alias for Page.
func (m *Memory) Lookup(ref PageRef) (Page, bool) { return m.Page(ref) }

// Pages returns all live virtual pages ordered by page ID.
func (m *Memory) Pages() []Page {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Page, 0, len(m.mapSlots))
	for _, slot := range m.mapSlots {
		if slot.inUse && slot.page != nil {
			out = append(out, m.snapshotVirtual(slot.page))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Memory) snapshotVirtual(page *virtualPage) Page {
	var refs int
	var physicalGeneration Generation
	if physical := m.lookupPhysical(page.physical); physical != nil {
		refs = physical.refs
		physicalGeneration = physical.generation
	}
	tags := page.tags.Clone()
	return Page{
		ID:                     page.id,
		Generation:             page.generation,
		DomainID:               page.domain,
		Digest:                 page.digest,
		Tags:                   tags,
		SensitiveTags:          tags.Clone(),
		Shareable:              page.shareable,
		PhysicalPageID:         page.physical,
		PhysicalPageGeneration: physicalGeneration,
		RefCount:               refs,
		Content:                append([]byte(nil), page.content...),
	}
}

func (m *Memory) snapshotPhysicalResident(physical *physicalPage) Page {
	// Policy sees the aggregate state of every virtual page mapping the
	// physical page.  This prevents a clean incoming page from sharing with a
	// physical page that also contains a sensitive, unshareable or foreign
	// mapping installed earlier by a permissive policy.
	var chosen *virtualPage
	var chosenID PageID
	tags := make(TagSet)
	commonDomain := DomainID("")
	firstDomain := true
	allShareable := true
	for id := range physical.members {
		if int(id) >= len(m.mapSlots) {
			continue
		}
		slot := &m.mapSlots[id]
		if !slot.inUse || slot.page == nil {
			continue
		}
		if chosen == nil || id < chosenID {
			chosen = slot.page
			chosenID = id
		}
		for tag := range slot.page.tags {
			tags[tag] = struct{}{}
		}
		if firstDomain {
			commonDomain = slot.page.domain
			firstDomain = false
		} else if slot.page.domain != commonDomain {
			commonDomain = ""
		}
		if !slot.page.shareable {
			allShareable = false
		}
	}
	if chosen == nil {
		return Page{
			Digest:                 physical.digest,
			PhysicalPageID:         physical.id,
			PhysicalPageGeneration: physical.generation,
			RefCount:               physical.refs,
			Content:                append([]byte(nil), physical.content...),
		}
	}
	return Page{
		ID:                     chosen.id,
		Generation:             chosen.generation,
		DomainID:               commonDomain,
		Digest:                 physical.digest,
		Tags:                   tags,
		SensitiveTags:          tags.Clone(),
		Shareable:              allShareable,
		PhysicalPageID:         physical.id,
		PhysicalPageGeneration: physical.generation,
		RefCount:               physical.refs,
		Content:                append([]byte(nil), chosen.content...),
	}
}

func (m *Memory) snapshotPhysical(physical *physicalPage) PhysicalPage {
	return PhysicalPage{
		ID:         physical.id,
		Generation: physical.generation,
		Digest:     physical.digest,
		RefCount:   physical.refs,
		COWCount:   physical.cowCount,
		Content:    append([]byte(nil), physical.content...),
	}
}

// PhysicalPage returns a snapshot of a live physical page.
func (m *Memory) PhysicalPage(id PhysicalPageID) (PhysicalPage, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	physical := m.lookupPhysical(id)
	if physical == nil || physical.refs == 0 {
		return PhysicalPage{}, false
	}
	return m.snapshotPhysical(physical), true
}

// PhysicalPages returns all live physical pages ordered by ID.
func (m *Memory) PhysicalPages() []PhysicalPage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PhysicalPage, 0, len(m.physicalSlots))
	for _, slot := range m.physicalSlots {
		if slot.inUse && slot.page != nil && slot.page.refs > 0 {
			out = append(out, m.snapshotPhysical(slot.page))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RefCount returns the physical reference count observed through ref.
func (m *Memory) RefCount(ref PageRef) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	page, err := m.resolve(ref)
	if err != nil {
		return 0
	}
	if physical := m.lookupPhysical(page.physical); physical != nil {
		return physical.refs
	}
	return 0
}

// PhysicalRefCount returns the current reference count for a physical ID.
func (m *Memory) PhysicalRefCount(id PhysicalPageID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if physical := m.lookupPhysical(id); physical != nil {
		return physical.refs
	}
	return 0
}

// Read performs a non-mutating access.  It never triggers COW.
func (m *Memory) Read(ref PageRef) (ReadResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	page, err := m.resolve(ref)
	if err != nil {
		return ReadResult{}, err
	}
	cost := m.costs.For(EventAccess)
	obs := Observation{
		Event:          EventAccess,
		PageID:         page.id,
		Generation:     page.generation,
		PhysicalPageID: page.physical,
		Cost:           cost,
	}
	m.stats.Accesses++
	return ReadResult{
		Content:     append([]byte(nil), page.content...),
		Cost:        cost,
		Observation: obs,
	}, nil
}

// Write writes to a page.  If the page currently shares a physical page, the
// first write detaches it with a COW event.  Later writes are private writes.
func (m *Memory) Write(ref PageRef, data []byte) (WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeLocked(ref, data)
}

func (m *Memory) writeLocked(ref PageRef, data []byte) (WriteResult, error) {
	page, err := m.resolve(ref)
	if err != nil {
		return WriteResult{}, err
	}
	physical := m.lookupPhysical(page.physical)
	if physical == nil {
		return WriteResult{}, ErrStalePage
	}

	result := WriteResult{
		PhysicalPageID: page.physical,
		Generation:     page.generation,
		BytesWritten:   len(data),
	}

	if physical.refs > 1 {
		if fault := m.consumeCOWFault(page); fault != nil {
			cost := m.costs.For(EventCOWFailure)
			obs := Observation{
				Event:          EventCOWFailure,
				PageID:         page.id,
				Generation:     page.generation,
				PhysicalPageID: page.physical,
				Cost:           cost,
			}
			m.stats.COWFailures++
			result.COW = true
			result.COWFailed = true
			result.Cost = cost
			result.Observation = obs
			return result, fault
		}

		m.detach(page)
		newPhysical := m.allocatePhysical(page.digest, page.content)
		newPhysical.refs = 1
		newPhysical.members[page.id] = struct{}{}
		newPhysical.cowCount++
		page.physical = newPhysical.id

		cost := m.costs.For(EventCOW)
		m.stats.COWEvents++
		result.COW = true
		result.Cost = cost
		result.PhysicalPageID = newPhysical.id
	} else {
		cost := m.costs.For(EventPrivateWrite)
		m.stats.PrivateWrites++
		result.Cost = cost
	}

	page.content = append(page.content[:0], data...)
	if current := m.lookupPhysical(page.physical); current != nil {
		current.content = append(current.content[:0], page.content...)
	}
	result.Observation = Observation{
		Event:          writeEvent(result.COW),
		PageID:         page.id,
		Generation:     page.generation,
		PhysicalPageID: page.physical,
		Cost:           result.Cost,
	}
	return result, nil
}

func (m *Memory) consumeCOWFault(page *virtualPage) error {
	snapshot := m.snapshotVirtual(page)
	if m.cowFault != nil {
		if err := m.cowFault(snapshot); err != nil {
			return err
		}
	}
	if m.failCOWOnce {
		m.failCOWOnce = false
		err := m.failCOWErr
		if err == nil {
			err = ErrCOWFailed
		}
		return err
	}
	return nil
}

// UpdateContent detaches a shared page first, then replaces its content and
// recomputes its digest.  Updated pages stay private; they are not silently
// re-deduplicated.
func (m *Memory) UpdateContent(ref PageRef, content []byte) (WriteResult, error) {
	return m.UpdateContentDigest(ref, content, "")
}

// UpdateContentDigest is UpdateContent with an explicit digest override.
func (m *Memory) UpdateContentDigest(ref PageRef, content []byte, digest Digest) (WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	page, err := m.resolve(ref)
	if err != nil {
		return WriteResult{}, err
	}
	physical := m.lookupPhysical(page.physical)
	if physical == nil {
		return WriteResult{}, ErrStalePage
	}

	result := WriteResult{
		PhysicalPageID: page.physical,
		Generation:     page.generation,
		BytesWritten:   len(content),
	}
	if physical.refs > 1 {
		if fault := m.consumeCOWFault(page); fault != nil {
			cost := m.costs.For(EventCOWFailure)
			m.stats.COWFailures++
			result.COW = true
			result.COWFailed = true
			result.Cost = cost
			result.Observation = Observation{
				Event:          EventCOWFailure,
				PageID:         page.id,
				Generation:     page.generation,
				PhysicalPageID: page.physical,
				Cost:           cost,
			}
			return result, fault
		}
		m.detach(page)
		newPhysical := m.allocatePhysical(page.digest, page.content)
		newPhysical.refs = 1
		newPhysical.members[page.id] = struct{}{}
		newPhysical.cowCount++
		page.physical = newPhysical.id
		cost := m.costs.For(EventCOW)
		m.stats.COWEvents++
		result.COW = true
		result.Cost = cost
		result.PhysicalPageID = newPhysical.id
	} else {
		result.Cost = m.costs.For(EventPrivateWrite)
		m.stats.PrivateWrites++
	}

	page.content = append(page.content[:0], content...)
	if digest == "" {
		digest = m.digest(content)
	}
	page.digest = digest
	if current := m.lookupPhysical(page.physical); current != nil {
		current.content = append(current.content[:0], page.content...)
		current.digest = digest
	}
	result.Observation = Observation{
		Event:          writeEvent(result.COW),
		PageID:         page.id,
		Generation:     page.generation,
		PhysicalPageID: page.physical,
		Cost:           result.Cost,
	}
	return result, nil
}

// UpdateTags replaces the page's sensitive tags.  A shared page is detached
// before the metadata write.
func (m *Memory) UpdateTags(ref PageRef, tags []string) (WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateMetadataLocked(ref, func(page *virtualPage) {
		page.tags = NewTagSet(tags...)
	})
}

// SetTags is an alias for UpdateTags.
func (m *Memory) SetTags(ref PageRef, tags []string) (WriteResult, error) {
	return m.UpdateTags(ref, tags)
}

// AddTag adds a sensitive tag, detaching a shared page first.
func (m *Memory) AddTag(ref PageRef, tag string) (WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateMetadataLocked(ref, func(page *virtualPage) {
		if tag != "" {
			page.tags[tag] = struct{}{}
		}
	})
}

// RemoveTag removes a sensitive tag, detaching a shared page first.
func (m *Memory) RemoveTag(ref PageRef, tag string) (WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateMetadataLocked(ref, func(page *virtualPage) {
		delete(page.tags, tag)
	})
}

// SetShareable updates the explicit share flag.  Clearing it detaches a shared
// page so the physical page no longer contains a non-shareable mapping.
func (m *Memory) SetShareable(ref PageRef, shareable bool) (WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateMetadataLocked(ref, func(page *virtualPage) {
		page.shareable = shareable
	})
}

func (m *Memory) updateMetadataLocked(ref PageRef, mutate func(*virtualPage)) (WriteResult, error) {
	page, err := m.resolve(ref)
	if err != nil {
		return WriteResult{}, err
	}
	physical := m.lookupPhysical(page.physical)
	if physical == nil {
		return WriteResult{}, ErrStalePage
	}

	result := WriteResult{
		PhysicalPageID: page.physical,
		Generation:     page.generation,
	}
	if physical.refs > 1 {
		if fault := m.consumeCOWFault(page); fault != nil {
			cost := m.costs.For(EventCOWFailure)
			m.stats.COWFailures++
			result.COW = true
			result.COWFailed = true
			result.Cost = cost
			result.Observation = Observation{
				Event:          EventCOWFailure,
				PageID:         page.id,
				Generation:     page.generation,
				PhysicalPageID: page.physical,
				Cost:           cost,
			}
			return result, fault
		}
		m.detach(page)
		newPhysical := m.allocatePhysical(page.digest, page.content)
		newPhysical.refs = 1
		newPhysical.members[page.id] = struct{}{}
		newPhysical.cowCount++
		page.physical = newPhysical.id
		result.COW = true
		result.Cost = m.costs.For(EventCOW)
		m.stats.COWEvents++
		result.PhysicalPageID = newPhysical.id
	} else {
		result.Cost = m.costs.For(EventPrivateWrite)
		m.stats.PrivateWrites++
	}

	mutate(page)
	result.Observation = Observation{
		Event:          writeEvent(result.COW),
		PageID:         page.id,
		Generation:     page.generation,
		PhysicalPageID: page.physical,
		Cost:           result.Cost,
	}
	return result, nil
}

func writeEvent(cow bool) EventType {
	if cow {
		return EventCOW
	}
	return EventPrivateWrite
}

// Delete releases a virtual page.  The physical page is reclaimed only when
// its reference count reaches zero.
func (m *Memory) Delete(ref PageRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	page, err := m.resolve(ref)
	if err != nil {
		return err
	}
	if physical := m.lookupPhysical(page.physical); physical != nil {
		delete(physical.members, page.id)
		physical.refs--
		if physical.refs == 0 {
			m.reclaimPhysical(physical)
		}
	}
	slot := &m.mapSlots[page.id]
	slot.page = nil
	slot.inUse = false
	m.freeMaps = append(m.freeMaps, page.id)
	return nil
}

// Close is an alias for Delete.
func (m *Memory) Close(ref PageRef) error { return m.Delete(ref) }

// Release is an alias for Delete.
func (m *Memory) Release(ref PageRef) error { return m.Delete(ref) }

// String returns a short diagnostic description.
func (m *Memory) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("memory pages=%d physical=%d policy=%s", m.now().LivePages, m.now().LivePhysicalPages, m.policy.Name())
}

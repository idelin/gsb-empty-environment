package zdebug

import (
	"container/list"
	"fmt"
	"sync"
)

type pageKey struct {
	state *sectionState
	index int
}

type cacheEntry struct {
	key  pageKey
	page []byte
}

// budget is the globally shared byte budget. It owns the authoritative page
// cache: a page is reachable by readers only while it remains in the LRU.
type budget struct {
	mu    sync.Mutex
	limit int64 // zero means unlimited
	used  int64

	lru   *list.List
	index map[pageKey]*list.Element
}

func newBudget(limit int64) *budget {
	return &budget{
		limit: limit,
		lru:   list.New(),
		index: make(map[pageKey]*list.Element),
	}
}

// getPage returns a cached page and marks it most-recently-used.
func (b *budget) getPage(state *sectionState, index int) ([]byte, bool) {
	key := pageKey{state: state, index: index}

	b.mu.Lock()
	defer b.mu.Unlock()

	element, ok := b.index[key]
	if !ok {
		return nil, false
	}
	b.lru.MoveToBack(element)
	return element.Value.(*cacheEntry).page, true
}

// commitPage adds one freshly verified page for state. Cached pages of every
// section, including older pages of state itself, are evicted LRU-first
// until the new page fits; the just-installed target page is never evicted.
// If even an empty cache cannot hold it, the install is rejected.
func (b *budget) commitPage(state *sectionState, index int, page []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := pageKey{state: state, index: index}
	if _, exists := b.index[key]; exists {
		return nil
	}

	size := int64(len(page))
	for b.limit > 0 && b.used+size > b.limit {
		if !b.evictForLocked(key) {
			return fmt.Errorf("%w: section %q needs %d-byte page, limit %d",
				ErrBudgetExhausted, state.section.Name, size, b.limit)
		}
	}

	element := b.lru.PushBack(&cacheEntry{key: key, page: page})
	b.index[key] = element
	b.used += size
	return nil
}

// evictForLocked evicts one page to make room for keep. Other sections are
// evicted first so a section's own verified pages survive cross-section
// pressure; only when the whole budget belongs to this section do we drop an
// older page of it. The target key itself is never evicted.
func (b *budget) evictForLocked(keep pageKey) bool {
	if element := b.oldestOtherSectionLocked(keep.state); element != nil {
		b.dropLocked(element)
		return true
	}
	if element := b.oldestSameSectionExceptLocked(keep); element != nil {
		b.dropLocked(element)
		return true
	}
	return false
}

func (b *budget) oldestOtherSectionLocked(keep *sectionState) *list.Element {
	for element := b.lru.Front(); element != nil; element = element.Next() {
		entry := element.Value.(*cacheEntry)
		if entry.key.state != keep {
			return element
		}
	}
	return nil
}

func (b *budget) oldestSameSectionExceptLocked(keep pageKey) *list.Element {
	for element := b.lru.Front(); element != nil; element = element.Next() {
		entry := element.Value.(*cacheEntry)
		if entry.key.state == keep.state && entry.key != keep {
			return element
		}
	}
	return nil
}

func (b *budget) dropLocked(element *list.Element) {
	entry := b.lru.Remove(element).(*cacheEntry)
	delete(b.index, entry.key)
	b.used -= int64(len(entry.page))
}

// removeSection drops every cached page owned by state.
func (b *budget) removeSection(state *sectionState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removeSectionLocked(state)
}

func (b *budget) removeSectionLocked(state *sectionState) {
	// Walk the authoritative LRU list; mutating the index map during a
	// map range would be legal but would desynchronize bookkeeping.
	for element := b.lru.Front(); element != nil; {
		next := element.Next()
		entry := element.Value.(*cacheEntry)
		if entry.key.state == state {
			b.dropLocked(element)
		}
		element = next
	}
}

// purgeAll empties the cache, for example on Host.Close.
func (b *budget) purgeAll() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lru.Init()
	b.index = make(map[pageKey]*list.Element)
	b.used = 0
}

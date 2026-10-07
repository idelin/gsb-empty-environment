package viewbuilder

import (
	"sort"
	"sync"
)

// View is a servable materialized view. Public queries take a read
// lock, so readers always observe a consistent state; the builder
// swaps the serving View pointer atomically at switch time.
type View struct {
	mu   sync.RWMutex
	data map[string]string
	seq  uint64 // watermark: every event with Seq <= seq is applied
	gen  uint64
}

func newView(gen uint64) *View {
	return &View{data: make(map[string]string), gen: gen}
}

// Get returns the value for key.
func (v *View) Get(key string) (string, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	val, ok := v.data[key]
	return val, ok
}

// Len returns the number of live keys.
func (v *View) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.data)
}

// Keys returns all live keys in sorted order.
func (v *View) Keys() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	keys := make([]string, 0, len(v.data))
	for k := range v.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Range iterates the view in sorted key order until f returns false.
func (v *View) Range(f func(k, val string) bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	keys := make([]string, 0, len(v.data))
	for k := range v.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !f(k, v.data[k]) {
			return
		}
	}
}

// Seq returns the applied watermark.
func (v *View) Seq() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.seq
}

// Generation returns the build generation of this view.
func (v *View) Generation() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.gen
}

// apply mutates the view with one in-order event.
func (v *View) apply(ev Event) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if ev.Op == OpPut {
		v.data[ev.Key] = ev.Value
	} else {
		delete(v.data, ev.Key)
	}
	v.seq = ev.Seq
}

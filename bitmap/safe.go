package bitmap

import "sync"

// SafeBitmap wraps a Bitmap with a RWMutex for concurrent use.
type SafeBitmap struct {
	mu sync.RWMutex
	b  *Bitmap
}

// NewSafe returns an empty concurrency-safe bitmap.
func NewSafe() *SafeBitmap { return &SafeBitmap{b: New()} }

func (s *SafeBitmap) Add(id uint64) {
	s.mu.Lock()
	s.b.Add(id)
	s.mu.Unlock()
}

func (s *SafeBitmap) AddRange(lo, hi uint64) {
	s.mu.Lock()
	s.b.AddRange(lo, hi)
	s.mu.Unlock()
}

func (s *SafeBitmap) Contains(id uint64) bool {
	s.mu.RLock()
	ok := s.b.Contains(id)
	s.mu.RUnlock()
	return ok
}

func (s *SafeBitmap) Cardinality() uint64 {
	s.mu.RLock()
	n := s.b.Cardinality()
	s.mu.RUnlock()
	return n
}

func (s *SafeBitmap) Optimize() {
	s.mu.Lock()
	s.b.Optimize()
	s.mu.Unlock()
}

// Snapshot returns a deep copy consistent at call time.
func (s *SafeBitmap) Snapshot() *Bitmap {
	s.mu.RLock()
	out := s.b.Clone()
	s.mu.RUnlock()
	return out
}

// And returns s ∩ o without mutating either.
func (s *SafeBitmap) And(o *Bitmap) *Bitmap {
	s.mu.RLock()
	out := s.b.And(o)
	s.mu.RUnlock()
	return out
}

// Or returns s ∪ o without mutating either.
func (s *SafeBitmap) Or(o *Bitmap) *Bitmap {
	s.mu.RLock()
	out := s.b.Or(o)
	s.mu.RUnlock()
	return out
}

// Xor returns s △ o without mutating either.
func (s *SafeBitmap) Xor(o *Bitmap) *Bitmap {
	s.mu.RLock()
	out := s.b.Xor(o)
	s.mu.RUnlock()
	return out
}

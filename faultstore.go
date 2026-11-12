package netsec

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

// FaultStore wraps durable storage with faults a real host can exhibit:
// injected I/O errors, acknowledged writes whose durable commit is delayed,
// and a crash that discards every write that never reached stable storage.
// Delayed puts are the test hook for "late" durability: they are reported as
// successful to the writer but stay invisible to reads and listings until
// DeliverPending or FlushPending moves them into the committed view.
type FaultStore struct {
	committed *MemoryStore

	mu        sync.Mutex
	pending   map[string][]byte
	failPut   func(key string) bool
	failGet   func(key string) bool
	failDel   func(key string) bool
	failList  func(prefix string) bool
	puts      int
	gets      int
	dels      int
	lists     int
	delayed   bool
	failAfter int
}

// NewFaultInjectingStore returns a fault store whose durable state starts
// empty and whose injected faults are initially disabled.
func NewFaultInjectingStore() *FaultStore {
	return &FaultStore{
		committed: NewMemoryStore(),
		pending:   make(map[string][]byte),
		failAfter: -1,
	}
}

// FailPut installs a predicate evaluated for every Put. A true result makes
// the write fail with a synthetic I/O error.
func (s *FaultStore) FailPut(fn func(key string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failPut = fn
}

// FailGet installs a predicate evaluated for every Get.
func (s *FaultStore) FailGet(fn func(key string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failGet = fn
}

// FailDelete installs a predicate evaluated for every Delete.
func (s *FaultStore) FailDelete(fn func(key string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failDel = fn
}

// FailList installs a predicate evaluated for every List.
func (s *FaultStore) FailList(fn func(prefix string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failList = fn
}

// DelayPuts makes every subsequent Put appear successful but withhold it from
// durable storage until pending writes are explicitly delivered.
func (s *FaultStore) DelayPuts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delayed = true
}

// StopDelayPuts turns off write withholding without flushing existing pending
// writes.
func (s *FaultStore) StopDelayPuts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delayed = false
}

// FailAfterNPuts injects a single I/O error on the (n+1)-th future Put that
// reaches the Put method. A negative n disables the injection.
func (s *FaultStore) FailAfterNPuts(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAfter = n
}

// PendingCount reports how many acknowledged writes are not yet durable.
func (s *FaultStore) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// Crash discards every uncommitted write, modelling a process restart where
// only the committed view survives.
func (s *FaultStore) Crash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = make(map[string][]byte)
}

// DeliverPending makes one withheld write durable. It returns false when no
// pending write exists, which lets tests deterministically model a single late
// confirmation arriving after recovery.
func (s *FaultStore) DeliverPending(ctx context.Context) bool {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return false
	}
	keys := make([]string, 0, len(s.pending))
	for key := range s.pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	key := keys[0]
	data := s.pending[key]
	delete(s.pending, key)
	s.mu.Unlock()

	_ = s.committed.Put(ctx, key, data)
	return true
}

// FlushPending makes every withheld write durable and returns how many were
// delivered.
func (s *FaultStore) FlushPending(ctx context.Context) int {
	s.mu.Lock()
	batch := make(map[string][]byte, len(s.pending))
	for key, data := range s.pending {
		batch[key] = append([]byte(nil), data...)
	}
	s.pending = make(map[string][]byte)
	delayed := s.delayed
	s.delayed = false
	s.mu.Unlock()

	keys := make([]string, 0, len(batch))
	for key := range batch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_ = s.committed.Put(ctx, key, batch[key])
	}
	s.mu.Lock()
	s.delayed = delayed
	s.mu.Unlock()
	return len(batch)
}

// Committed exposes the durable backing store so a test can construct a fresh
// coordinator over the same media after a crash.
func (s *FaultStore) Committed() *MemoryStore {
	return s.committed
}

// OpCounters returns total calls observed since construction: puts, gets,
// deletes and listings in that order.
func (s *FaultStore) OpCounters() (puts, gets, dels, lists int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puts, s.gets, s.dels, s.lists
}

// Get implements BlobStore.
func (s *FaultStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	fail := s.failGet != nil && s.failGet(key)
	s.gets++
	s.mu.Unlock()
	if fail {
		return nil, &faultIOError{op: "get", key: key}
	}
	return s.committed.Get(ctx, key)
}

// Put implements BlobStore.
func (s *FaultStore) Put(ctx context.Context, key string, data []byte) error {
	s.mu.Lock()
	fail := s.failPut != nil && s.failPut(key)
	if !fail && s.failAfter >= 0 && s.puts >= s.failAfter {
		fail = true
		s.failAfter = -1
	}
	delayed := s.delayed
	s.puts++
	if fail {
		s.mu.Unlock()
		return &faultIOError{op: "put", key: key}
	}
	if delayed {
		s.pending[key] = append([]byte(nil), data...)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.committed.Put(ctx, key, append([]byte(nil), data...))
}

// Delete implements BlobStore.
func (s *FaultStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	fail := s.failDel != nil && s.failDel(key)
	s.dels++
	if fail {
		s.mu.Unlock()
		return &faultIOError{op: "delete", key: key}
	}
	// A delete also cancels any withheld write for the same key.
	delete(s.pending, key)
	s.mu.Unlock()
	return s.committed.Delete(ctx, key)
}

// List implements BlobStore.
func (s *FaultStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	fail := s.failList != nil && s.failList(prefix)
	s.lists++
	s.mu.Unlock()
	if fail {
		return nil, &faultIOError{op: "list", key: prefix}
	}
	keys, err := s.committed.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	for key := range s.pending {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	s.mu.Unlock()
	sort.Strings(keys)
	return keys, nil
}

type faultIOError struct {
	op  string
	key string
}

func (e *faultIOError) Error() string {
	return "netsec: injected I/O error during " + e.op + " of " + e.key
}

// IsFaultIOError reports whether err is an injected storage I/O fault.
func IsFaultIOError(err error) bool {
	var target *faultIOError
	return errors.As(err, &target)
}

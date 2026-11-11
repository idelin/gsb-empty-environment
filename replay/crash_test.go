package replay

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// crashDrive repeatedly presents keys through freshly constructed caches,
// simulating processes that restart after a simulated power loss. When reopen
// is true each restart also reopens the backing handle (file store); when false
// the same backing store survives and only the cache is rebuilt (memory store).
// Every key must be accepted exactly once regardless of where faults land.
func crashDrive(
	t *testing.T,
	open func(t *testing.T) (Storage, FaultHookSource),
	reopen bool,
	keyCount int,
) {
	t.Helper()
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)

	success := make(map[int]int)
	replay := make(map[int]int)

	// Every accepted key produces two durable appends (entry + watermark), so
	// the uninterrupted log contains keyCount*2 append points.
	for target := 1; target <= keyCount*2; target++ {
		t.Run(fmt.Sprintf("faultAtOrdinal%d", target), func(t *testing.T) {
			for _, fault := range []Fault{FaultBeforeWrite, FaultAfterWrite, FaultTornWrite} {
				faultName := map[Fault]string{
					FaultBeforeWrite: "before",
					FaultAfterWrite:  "after",
					FaultTornWrite:   "torn",
				}[fault]
				t.Run(faultName, func(t *testing.T) {
					baseStorage, baseSource := open(t)
					fired := false
					hook := FaultHook(nil)
					if !reopen {
						hook = FaultHook(func(ordinal int, rec Record) (Fault, bool) {
							if !fired && ordinal == target {
								fired = true
								return fault, true
							}
							return 0, false
						})
					}

					localSuccess := map[int]int{}
					localReplay := map[int]int{}

					for keyIndex := 0; keyIndex < keyCount; keyIndex++ {
						for attempt := 0; attempt < 6; attempt++ {
							storage, hookSource := baseStorage, baseSource
							if reopen {
								storage, hookSource = open(t)
							}
							clock := newFakeClock(base)
							if reopen {
								alreadyFired := fired
								hookSource.SetHook(func(ordinal int, rec Record) (Fault, bool) {
									if !alreadyFired && ordinal == target {
										alreadyFired = true
										fired = true
										return fault, true
									}
									return 0, false
								})
							} else {
								hookSource.SetHook(hook)
							}
							cache, err := New(Config{
								Storage:         storage,
								Clock:           clock,
								PastWindow:      time.Hour,
								FutureTolerance: time.Hour,
							})
							if err != nil {
								t.Fatalf("New: %v", err)
							}
							if !reopen {
								cache.WithoutStorageClose()
							}
							key := testKey(clock, uint32(keyIndex+1))
							err = cache.Accept(ctx, key)
							closeErr := cache.Close()

							switch {
							case err == nil:
								if closeErr != nil {
									t.Fatalf("Close after success: %v", closeErr)
								}
								localSuccess[keyIndex]++
								attempt = 6
							case errors.Is(err, ErrReplay):
								localReplay[keyIndex]++
								attempt = 6
							case errors.Is(err, ErrPowerLoss):
								if closeErr != nil && !errors.Is(closeErr, ErrPowerLoss) {
									t.Fatalf("Close after power loss: %v", closeErr)
								}
								if !reopen {
									if reopener, ok := baseSource.(interface{ Reopen() }); ok {
										reopener.Reopen()
									}
								}
								// Restart and retry the same key.
								continue
							default:
								t.Fatalf("key %d attempt %d: unexpected error %v", keyIndex, attempt, err)
							}
						}
					}

					for keyIndex := 0; keyIndex < keyCount; keyIndex++ {
						if localSuccess[keyIndex]+localReplay[keyIndex] != 1 {
							t.Fatalf("key %d: success=%d replay=%d, want exactly one terminal outcome",
								keyIndex, localSuccess[keyIndex], localReplay[keyIndex])
						}
						success[keyIndex] += localSuccess[keyIndex]
						replay[keyIndex] += localReplay[keyIndex]
					}
					if !reopen {
						if reopener, ok := baseSource.(interface{ Reopen() }); ok {
							reopener.Reopen()
						}
					}

					if reopen {
						auditStorage, auditSource := open(t)
						defer auditStorage.Close()
						auditSource.SetHook(nil)
						auditDurableLog(t, auditStorage, keyCount)
					} else {
						baseSource.SetHook(nil)
						auditDurableLog(t, baseStorage, keyCount)
					}
				})
			}
		})
	}

	// Across all fault placements and fault modes, every key must have been
	// accepted in some runs and rejected as replay in others, never duplicated.
	for keyIndex := 0; keyIndex < keyCount; keyIndex++ {
		if success[keyIndex] == 0 {
			t.Fatalf("key %d was never accepted across crash scenarios", keyIndex)
		}
		if replay[keyIndex] == 0 {
			t.Fatalf("key %d was never observed as replay after a crash", keyIndex)
		}
	}
}

func auditDurableLog(t *testing.T, storage Storage, keyCount int) {
	t.Helper()
	if locker, ok := storage.(interface {
		Lock(context.Context) error
		Unlock(context.Context) error
	}); ok {
		if err := locker.Lock(context.Background()); err != nil {
			t.Fatalf("audit Lock: %v", err)
		}
		defer func() { _ = locker.Unlock(context.Background()) }()
	}
	records, err := storage.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll after crashes: %v", err)
	}
	entries, watermarks := countEntries(records)
	if entries != keyCount {
		t.Fatalf("durable entries = %d, want %d (no duplicate acceptance)", entries, keyCount)
	}
	if watermarks < keyCount {
		t.Fatalf("durable watermarks = %d, want at least %d (one per acceptance)", watermarks, keyCount)
	}

	seen := make(map[string]bool, entries)
	var maxBound int64
	var monotonic []int64
	for _, rec := range records {
		switch rec.Kind {
		case KindEntry:
			if rec.Entry == nil || seen[rec.Entry.ID] {
				t.Fatalf("duplicate or malformed entry: %+v", rec.Entry)
			}
			seen[rec.Entry.ID] = true
		case KindWatermark:
			if rec.Watermark == nil {
				t.Fatal("malformed watermark record")
			}
			if rec.Watermark.LowerBoundUnixNano < maxBound {
				t.Fatalf("watermark regressed: %d < %d", rec.Watermark.LowerBoundUnixNano, maxBound)
			}
			maxBound = rec.Watermark.LowerBoundUnixNano
			monotonic = append(monotonic, maxBound)
		}
	}
}

// FaultHookSource allows the test driver to install a hook after the store was
// constructed.
type FaultHookSource interface {
	SetHook(hook FaultHook)
}

type memoryHookSource struct {
	storage *FaultyStorage
	inner   *MemoryStorage
}

func (m *memoryHookSource) SetHook(hook FaultHook) { m.storage.hook = hook }

func (m *memoryHookSource) Reopen() { m.inner.Reopen() }

func TestCrashPowerLossMemoryStorage(t *testing.T) {
	crashDrive(t, func(t *testing.T) (Storage, FaultHookSource) {
		inner := NewMemoryStorage()
		faulty := NewFaultyStorage(inner, nil)
		return faulty, &memoryHookSource{storage: faulty, inner: inner}
	}, false, 3)
}

type fileHookSource struct{ storage *FileStorage }

func (f *fileHookSource) SetHook(hook FaultHook) { f.storage.hook = hook }

func TestCrashPowerLossFileStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.log")
	crashDrive(t, func(t *testing.T) (Storage, FaultHookSource) {
		storage, err := OpenFileStorage(FileConfig{Path: path})
		if err != nil {
			t.Fatalf("OpenFileStorage: %v", err)
		}
		return storage, &fileHookSource{storage: storage}
	}, true, 3)
}

func TestTornTailIsRepairedOnNextLock(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	path := t.TempDir() + "/replay.log"

	storage, err := OpenFileStorage(FileConfig{Path: path})
	if err != nil {
		t.Fatalf("OpenFileStorage: %v", err)
	}
	cache := newTestCache(t, storage, clock)
	key := testKey(clock, 1)
	if err := cache.Accept(ctx, key); err != nil {
		t.Fatalf("Accept baseline: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen with a torn-write fault on the first append of a new handle.
	damaged, err := OpenFileStorage(FileConfig{
		Path: path,
		Hook: func(ordinal int, rec Record) (Fault, bool) {
			// Two records (entry + watermark) were durably appended above;
			// ordinal numbering continues from the persisted log.
			if ordinal == 3 {
				return FaultTornWrite, true
			}
			return 0, false
		},
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	tornKey := testKey(clock, 2)
	err = damaged.Lock(ctx)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	err = damaged.Append(ctx, Record{Kind: KindEntry, Entry: &EntryRecord{ID: tornKey.ID()}})
	if !errors.Is(err, ErrPowerLoss) {
		t.Fatalf("torn Append = %v, want ErrPowerLoss", err)
	}
	if err := damaged.Unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := damaged.Close(); err != nil {
		t.Fatalf("Close damaged: %v", err)
	}

	// The torn append used ordinal 1 of this handle and must have been
	// discarded entirely by the repair on the next lock.
	recoveredBefore, err := OpenFileStorage(FileConfig{Path: path})
	if err != nil {
		t.Fatalf("reopen after torn write: %v", err)
	}
	if err := recoveredBefore.Lock(ctx); err != nil {
		t.Fatalf("Lock recovered: %v", err)
	}
	existing, err := recoveredBefore.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll recovered: %v", err)
	}
	if entries, _ := countEntries(existing); entries != 1 {
		t.Fatalf("entries after repair = %d, want 1", entries)
	}
	if err := recoveredBefore.Unlock(ctx); err != nil {
		t.Fatalf("Unlock recovered: %v", err)
	}
	if err := recoveredBefore.Close(); err != nil {
		t.Fatalf("Close recoveredBefore: %v", err)
	}

	// A third handle must transparently remove the torn tail and keep working.
	recovered, err := OpenFileStorage(FileConfig{Path: path})
	if err != nil {
		t.Fatalf("reopen after torn write: %v", err)
	}
	recoveredCache := newTestCache(t, recovered, clock)
	if err := recoveredCache.Accept(ctx, key); !errors.Is(err, ErrReplay) {
		t.Fatalf("baseline key after torn write = %v, want ErrReplay", err)
	}
	if err := recoveredCache.Accept(ctx, tornKey); err != nil {
		t.Fatalf("torn key retry after repair = %v", err)
	}
}

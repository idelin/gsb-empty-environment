package netsec

import (
	"context"
	"slices"
	"sync"
)

// Cache is a concurrency-safe store of detection conclusions keyed by content
// digest and rule version. The zero value is not usable; construct one with
// NewCache.
type Cache struct {
	mu      sync.RWMutex
	entries map[Key]*Conclusion
}

// NewCache creates an empty cache.
func NewCache() *Cache {
	return &Cache{entries: make(map[Key]*Conclusion)}
}

// Put stores conclusion, merging it with any existing entry for the same Key.
// It returns the merged conclusion. Conclusions with different keys are kept
// in separate entries, so a rule upgrade never overwrites or contaminates
// conclusions produced under an older rule set.
func (c *Cache) Put(conclusion *Conclusion) (*Conclusion, error) {
	if conclusion == nil {
		return nil, ErrInvalidKey
	}
	if err := conclusion.validate(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	existing, ok := c.entries[conclusion.Key]
	if !ok {
		stored := conclusion.clone()
		c.entries[conclusion.Key] = stored
		return stored.clone(), nil
	}

	merged, err := Merge(existing, conclusion)
	if err != nil {
		return nil, err
	}
	c.entries[conclusion.Key] = merged
	return merged.clone(), nil
}

// Get returns the cached conclusion for key together with the requested checks
// that are still missing. The conclusion is only reusable when len(missing) ==
// 0, i.e. every requested check has executed. A missing entry and an
// incomplete entry are reported the same way: missing checks, never an
// implicit pass.
func (c *Cache) Get(key Key, requested []CheckID) (conclusion *Conclusion, missing []CheckID, err error) {
	if key.ContentDigest == "" || key.RulesVersion == "" {
		return nil, nil, ErrInvalidKey
	}

	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, dedupSorted(requested), nil
	}

	copyOf := entry.clone()
	return copyOf, copyOf.Missing(requested), nil
}

// Snapshot returns a deep copy of every stored conclusion, ordered by content
// digest and rule version. It is primarily intended for diagnostics and
// tests.
func (c *Cache) Snapshot() []*Conclusion {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]*Conclusion, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, entry.clone())
	}
	slices.SortFunc(out, func(a, b *Conclusion) int {
		if a.Key.ContentDigest != b.Key.ContentDigest {
			if a.Key.ContentDigest < b.Key.ContentDigest {
				return -1
			}
			return 1
		}
		if a.Key.RulesVersion < b.Key.RulesVersion {
			return -1
		}
		if a.Key.RulesVersion > b.Key.RulesVersion {
			return 1
		}
		return 0
	})
	return out
}

// Len reports the number of distinct keys in the cache.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// CheckFunc executes one detection check. Implementations must honor ctx and
// return promptly when it is canceled.
type CheckFunc func(ctx context.Context, check CheckID) (CheckResult, error)

// maxConcurrentChecks bounds the goroutines spawned by Analyze regardless of
// how many checks are requested.
const maxConcurrentChecks = 32

// Analyze runs the requested checks concurrently and assembles a conclusion
// for key. Checks that finish are recorded in Results; checks that do not
// finish because ctx is canceled (or return an error) are listed in Pending,
// so unfinished work is never reported as a negative result.
//
// When ctx is already canceled, Analyze returns immediately with every check
// pending. The returned error is ctx.Err() when the run did not complete all
// checks; the partial conclusion remains valid and can be cached or retried.
func Analyze(ctx context.Context, key Key, checks []CheckID, run CheckFunc) (*Conclusion, error) {
	if key.ContentDigest == "" || key.RulesVersion == "" {
		return nil, ErrInvalidKey
	}
	uniqueChecks := dedupSorted(checks)
	for _, check := range uniqueChecks {
		if check == "" {
			return nil, ErrInvalidCheck
		}
	}

	conclusion := &Conclusion{Key: key}
	if len(uniqueChecks) == 0 {
		return conclusion, nil
	}
	if err := ctx.Err(); err != nil {
		conclusion.Pending = append(conclusion.Pending, uniqueChecks...)
		return conclusion, err
	}

	type outcome struct {
		result CheckResult
		err    error
	}
	outcomes := make([]outcome, len(uniqueChecks))

	sem := make(chan struct{}, maxConcurrentChecks)
	var wg sync.WaitGroup
	for i, check := range uniqueChecks {
		wg.Add(1)
		go func(index int, id CheckID) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				outcomes[index] = outcome{err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			if err := ctx.Err(); err != nil {
				outcomes[index] = outcome{err: err}
				return
			}
			result, err := run(ctx, id)
			outcomes[index] = outcome{result: result, err: err}
		}(i, check)
	}
	wg.Wait()

	var runErr error
	for i, item := range outcomes {
		check := uniqueChecks[i]
		if item.err != nil {
			conclusion.Pending = append(conclusion.Pending, check)
			runErr = context.Cause(ctx)
			continue
		}
		if err := validateResult(check, item.result); err != nil {
			conclusion.Pending = append(conclusion.Pending, check)
			if runErr == nil {
				runErr = err
			}
			continue
		}
		conclusion.Results = append(conclusion.Results, item.result)
		if item.result.RanAt.After(conclusion.UpdatedAt) {
			conclusion.UpdatedAt = item.result.RanAt
		}
	}

	conclusion.Pending = dedupSorted(conclusion.Pending)
	if err := conclusion.validate(); err != nil {
		return nil, err
	}

	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return conclusion, ctxErr
		}
		return conclusion, runErr
	}
	return conclusion, ctx.Err()
}

// Ensure returns a reusable conclusion for key when the cache already covers
// every requested check. Otherwise it runs the missing checks concurrently via
// Analyze, merges the partial result into the cache, and returns the merged
// entry together with anything still missing (for example after
// cancellation). On a full hit no checks are executed.
func (c *Cache) Ensure(ctx context.Context, key Key, requested []CheckID, run CheckFunc) (*Conclusion, []CheckID, error) {
	entry, missing, err := c.Get(key, requested)
	if err != nil {
		return nil, nil, err
	}
	if len(missing) == 0 {
		return entry, nil, nil
	}

	partial, analyzeErr := Analyze(ctx, key, missing, run)
	if partial != nil {
		merged, putErr := c.Put(partial)
		if putErr != nil {
			return nil, nil, putErr
		}
		entry = merged
	}
	if entry == nil {
		return nil, missing, analyzeErr
	}

	missing = entry.Missing(requested)
	if analyzeErr != nil {
		return entry, missing, analyzeErr
	}
	return entry, missing, nil
}

func validateResult(expected CheckID, result CheckResult) error {
	if result.Check != expected {
		return ErrInvalidCheck
	}
	switch result.Verdict {
	case VerdictNegative, VerdictPositive:
	default:
		return ErrInvalidVerdict
	}
	return nil
}

func dedupSorted(checks []CheckID) []CheckID {
	seen := make(map[CheckID]struct{}, len(checks))
	out := make([]CheckID, 0, len(checks))
	for _, check := range checks {
		if _, ok := seen[check]; ok {
			continue
		}
		seen[check] = struct{}{}
		out = append(out, check)
	}
	slices.Sort(out)
	return out
}

package scheduler

import (
	"context"
	"sync"

	"bitmapsched/bitmap"
)

// Store is a multi-tenant bitmap store. Each tenant gets an isolated
// namespace of named bitmaps, and every mutation is admitted through the
// scheduler so per-tenant quotas apply.
type Store struct {
	sched *Scheduler
	mu    sync.RWMutex
	data  map[string]map[string]*bitmap.Bitmap
}

// NewStore creates a Store whose mutations are scheduled by s.
func NewStore(s *Scheduler) *Store {
	return &Store{sched: s, data: make(map[string]map[string]*bitmap.Bitmap)}
}

// Add inserts ids into the tenant's named bitmap. The work is submitted to
// the scheduler under the tenant's quota; unknown tenants are rejected.
func (st *Store) Add(tenant, name string, ids ...uint64) error {
	done, err := st.sched.Submit(&Task{
		TenantID: tenant,
		Cost:     uint64(len(ids)) + 1,
		Run: func(context.Context) error {
			st.mu.Lock()
			defer st.mu.Unlock()
			m := st.data[tenant]
			if m == nil {
				m = make(map[string]*bitmap.Bitmap)
				st.data[tenant] = m
			}
			bm := m[name]
			if bm == nil {
				bm = bitmap.New()
				m[name] = bm
			}
			for _, id := range ids {
				bm.Add(id)
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	return <-done
}

// Snapshot returns a deep copy of the tenant's named bitmap. The copy is
// fully isolated: mutating it does not affect the store.
func (st *Store) Snapshot(tenant, name string) (*bitmap.Bitmap, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	m := st.data[tenant]
	if m == nil {
		return nil, false
	}
	bm := m[name]
	if bm == nil {
		return nil, false
	}
	return bm.Clone(), true
}

// DeleteTenantData drops all bitmaps of a tenant.
func (st *Store) DeleteTenantData(tenant string) {
	st.mu.Lock()
	delete(st.data, tenant)
	st.mu.Unlock()
}

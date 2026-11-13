package netsec

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	StateVersion = 1
)

// RouterState is a crash-consistent export of committed cache views. Pending
// responses are intentionally excluded: after a crash a router can expose
// either the old committed view or a view that already passed full validation,
// never a partially applied one.
type RouterState struct {
	Version int                  `json:"version"`
	SavedAt time.Time            `json:"savedAt"`
	Caches  []CacheStateSnapshot `json:"caches"`
}

// CacheStateSnapshot is one complete committed view plus the retained session
// tombstone list.
type CacheStateSnapshot struct {
	CacheID         CacheID     `json:"cacheID"`
	Generation      uint64      `json:"generation"`
	Session         SessionID   `json:"session"`
	Serial          uint32      `json:"serial"`
	RootDigest      Digest      `json:"rootDigest"`
	VRPs            []VRP       `json:"vrps"`
	UpdatedAt       time.Time   `json:"updatedAt"`
	RetiredSessions []SessionID `json:"retiredSessions,omitempty"`
	NeedsResync     bool        `json:"needsResync,omitempty"`
}

// ExportState returns a coherent copy of all committed views.
func (r *Router) ExportState() RouterState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := RouterState{
		Version: StateVersion,
		SavedAt: r.now(),
	}
	ids := make([]CacheID, 0, len(r.stores))
	for cacheID := range r.stores {
		ids = append(ids, cacheID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, cacheID := range ids {
		state := r.stores[cacheID]
		if state.committed == nil {
			continue
		}
		entry := CacheStateSnapshot{
			CacheID:     cacheID,
			Generation:  state.generation,
			Session:     state.committed.session,
			Serial:      state.committed.serial,
			RootDigest:  state.committed.root,
			VRPs:        state.committed.copyVRPs(),
			UpdatedAt:   state.committed.updatedAt,
			NeedsResync: state.markStale,
		}
		entry.RetiredSessions = make([]SessionID, 0, len(state.retired))
		for session := range state.retired {
			entry.RetiredSessions = append(entry.RetiredSessions, session)
		}
		sort.Slice(entry.RetiredSessions, func(i, j int) bool {
			return entry.RetiredSessions[i].String() < entry.RetiredSessions[j].String()
		})
		snapshot.Caches = append(snapshot.Caches, entry)
	}
	return snapshot
}

// RestoreState validates the complete snapshot before swapping any live
// state. A corrupt file therefore leaves the old views intact. Restored
// transports are disconnected and get a new generation so old signed
// responses cannot be replayed.
func (r *Router) RestoreState(snapshot RouterState) error {
	if snapshot.Version != StateVersion {
		return fmt.Errorf("%w: version %d", ErrInvalidState, snapshot.Version)
	}
	if snapshot.SavedAt.IsZero() {
		return fmt.Errorf("%w: missing save time", ErrInvalidState)
	}

	type restoreEntry struct {
		state       *routerCache
		view        *committedView
		retired     map[SessionID]struct{}
		nextGen     uint64
		needsResync bool
	}
	var entries []restoreEntry
	seenCaches := make(map[CacheID]struct{}, len(snapshot.Caches))
	seenStates := make(map[*routerCache]struct{}, len(snapshot.Caches))

	r.mu.RLock()
	for _, state := range r.stores {
		if state.generation == math.MaxUint64 {
			r.mu.RUnlock()
			return fmt.Errorf("%w: generation overflow for %q", ErrInvalidState, state.cacheID)
		}
	}
	for _, cached := range snapshot.Caches {
		if cached.CacheID == "" {
			r.mu.RUnlock()
			return fmt.Errorf("%w: empty cache ID", ErrInvalidState)
		}
		if _, exists := seenCaches[cached.CacheID]; exists {
			r.mu.RUnlock()
			return fmt.Errorf("%w: duplicate cache %q", ErrInvalidState, cached.CacheID)
		}
		seenCaches[cached.CacheID] = struct{}{}
		state := r.stores[cached.CacheID]
		if state == nil || len(state.key) != ed25519.PublicKeySize {
			r.mu.RUnlock()
			return fmt.Errorf("%w: %q", ErrUnknownCache, cached.CacheID)
		}
		if cached.Session.IsZero() {
			r.mu.RUnlock()
			return fmt.Errorf("%w: zero session for %q", ErrInvalidState, cached.CacheID)
		}
		if cached.UpdatedAt.IsZero() {
			r.mu.RUnlock()
			return fmt.Errorf("%w: missing update time for %q", ErrInvalidState, cached.CacheID)
		}
		set, sorted, err := canonicalVRPSet(cached.VRPs)
		if err != nil {
			r.mu.RUnlock()
			return err
		}
		root, err := rootFromSorted(sorted)
		if err != nil {
			r.mu.RUnlock()
			return err
		}
		if root != cached.RootDigest {
			r.mu.RUnlock()
			return fmt.Errorf("%w: cache %q", ErrRootMismatch, cached.CacheID)
		}
		retired := make(map[SessionID]struct{}, len(cached.RetiredSessions))
		for _, session := range cached.RetiredSessions {
			if session.IsZero() {
				r.mu.RUnlock()
				return fmt.Errorf("%w: zero retired session for %q", ErrInvalidState, cached.CacheID)
			}
			if session == cached.Session {
				r.mu.RUnlock()
				return fmt.Errorf("%w: current session is retired for %q", ErrInvalidState, cached.CacheID)
			}
			retired[session] = struct{}{}
		}
		gen := cached.Generation
		if gen < state.generation {
			gen = state.generation
		}
		if gen == math.MaxUint64 {
			r.mu.RUnlock()
			return fmt.Errorf("%w: generation overflow for %q", ErrInvalidState, cached.CacheID)
		}
		entries = append(entries, restoreEntry{
			state:       state,
			view:        &committedView{session: cached.Session, serial: cached.Serial, root: root, set: set, sorted: sorted, updatedAt: cached.UpdatedAt},
			retired:     retired,
			nextGen:     gen + 1,
			needsResync: cached.NeedsResync,
		})
		seenStates[state] = struct{}{}
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, state := range r.stores {
		if _, restored := seenStates[state]; restored {
			continue
		}
		state.committed = nil
		state.retired = make(map[SessionID]struct{})
		state.generation++
		state.connected = false
		state.activeReq = 0
		state.pending = nil
		state.responseDone = false
		state.markStale = false
	}
	for _, entry := range entries {
		entry.state.committed = entry.view
		entry.state.retired = entry.retired
		entry.state.generation = entry.nextGen
		entry.state.connected = false
		entry.state.activeReq = 0
		entry.state.pending = nil
		entry.state.responseDone = false
		entry.state.markStale = entry.needsResync
	}
	return nil
}

// SaveState writes a snapshot atomically using a temporary file in the
// destination directory and rename. A crash before rename leaves the previous
// file intact.
func (r *Router) SaveState(path string) error {
	if path == "" {
		return fmt.Errorf("netsec: empty state path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("netsec: create state directory: %w", err)
	}
	snapshot := r.ExportState()
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("netsec: encode state: %w", err)
	}
	payload = append(payload, '\n')

	tmp, err := os.CreateTemp(dir, ".router-state-*")
	if err != nil {
		return fmt.Errorf("netsec: create temporary state: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("netsec: chmod state: %w", err)
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("netsec: write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("netsec: sync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("netsec: close state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("netsec: replace state: %w", err)
	}
	cleanup = false
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

// LoadState reads a state file and restores it atomically.
func (r *Router) LoadState(path string) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("netsec: read state: %w", err)
	}
	var snapshot RouterState
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return fmt.Errorf("%w: decode state: %v", ErrInvalidState, err)
	}
	return r.RestoreState(snapshot)
}

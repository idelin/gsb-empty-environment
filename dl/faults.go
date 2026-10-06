package dl

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Faults describes failure injections, normally loaded from a JSON fault
// file. Request counters are 1-based and count every Server.Fetch call.
type Faults struct {
	// ShortWriteOnRequests lists request numbers whose body is truncated
	// while the declared Content-Range stays intact (short write).
	ShortWriteOnRequests []int `json:"short_write_on_requests,omitempty"`
	// ExpandRangeOnRequests lists request numbers where the server answers
	// with a range larger than requested, producing overlapping responses.
	ExpandRangeOnRequests []int `json:"expand_range_on_requests,omitempty"`
	// CorruptOnRequests lists request numbers whose first payload byte is
	// flipped, creating inconsistent data for overlapping ranges.
	CorruptOnRequests []int `json:"corrupt_on_requests,omitempty"`
	// VersionChangeAfterRequests rotates the entity (new content + ETag)
	// once this many requests have been served. 0 disables.
	VersionChangeAfterRequests int `json:"version_change_after_requests,omitempty"`
	// StaleReplayAfterRequests re-injects the first captured response
	// (old ETag) once this many requests have been served. 0 disables.
	StaleReplayAfterRequests int `json:"stale_replay_after_requests,omitempty"`
	// CrashPoint arms a simulated crash inside checkpoint saving:
	// "before_write", "before_rename" or "after_rename".
	CrashPoint string `json:"crash_point,omitempty"`
	// CrashOnCheckpoint selects which checkpoint save (1-based) crashes.
	CrashOnCheckpoint int `json:"crash_on_checkpoint,omitempty"`

	mu             sync.Mutex
	crashConsumed  bool
	versionRotated bool
	staleReplayed  bool
}

// LoadFaults parses a fault file. A nil path yields no faults.
func LoadFaults(path string) (*Faults, error) {
	if path == "" {
		return &Faults{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load fault file: %w", err)
	}
	var f Faults
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse fault file %s: %w", path, err)
	}
	return &f, nil
}

func (f *Faults) intIn(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

// shouldRotate reports whether the entity version must change before
// serving request number reqNo. Rotates at most once.
func (f *Faults) shouldRotate(reqNo int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.VersionChangeAfterRequests > 0 && reqNo > f.VersionChangeAfterRequests && !f.versionRotated {
		f.versionRotated = true
		return true
	}
	return false
}

// shouldReplayStale reports whether request reqNo must carry a stale copy
// of the first captured response. Replays at most once.
func (f *Faults) shouldReplayStale(reqNo int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StaleReplayAfterRequests > 0 && reqNo == f.StaleReplayAfterRequests && !f.staleReplayed {
		f.staleReplayed = true
		return true
	}
	return false
}

// shouldCrash reports whether a crash fires at the given checkpoint hook
// for the saveCount-th save. Consumed once, so a "restarted" process runs
// clean.
func (f *Faults) shouldCrash(point string, saveCount int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.crashConsumed || f.CrashPoint != point || f.CrashOnCheckpoint <= 0 {
		return false
	}
	if saveCount == f.CrashOnCheckpoint {
		f.crashConsumed = true
		return true
	}
	return false
}

package applog

import "os"

// Reclaim deletes sealed segments whose records all lie below the retention
// watermark and returns the number of segments removed.
//
// Consistency contract with cursor updates:
//   - The watermark is computed from cursor state that is already durable
//     (every cursor mutation commits the manifest before returning), so
//     reclaim never deletes data a consumer has not durably acknowledged.
//   - The active (last) segment is never removed, even if fully consumed, so
//     appends always have a target and the high watermark stays anchored.
//   - Segment files are the source of truth and are rediscovered by directory
//     scan on Open; a crash at any point during Reclaim leaves either the old
//     or the new set of files, both consistent with the committed manifest.
func (l *Log) Reclaim() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, ErrClosed
	}
	wm := l.watermarkLocked()
	old := l.segments
	kept := old[:0]
	removed := 0
	var firstErr error
	for i, seg := range old {
		eligible := i != len(old)-1 && seg.end() <= wm
		if eligible && firstErr == nil {
			seg.f.Close()
			if err := os.Remove(seg.path); err != nil {
				firstErr = err // keep this segment and stop deleting
			} else {
				removed++
				continue
			}
		}
		kept = append(kept, seg) // active, below-watermark-not-reached, or after a fault
	}
	// Clear the tail so removed segments can be GC'd.
	for i := len(kept); i < len(old); i++ {
		old[i] = nil
	}
	l.segments = kept
	if removed > 0 && firstErr == nil {
		if err := syncDir(l.dir); err != nil {
			return removed, err
		}
	}
	return removed, firstErr
}

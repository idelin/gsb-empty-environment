package sortexec

import "sync/atomic"

// Tracker counts live resources held by an Executor: open spill-file handles
// and open merge iterators. After a run finishes or is torn down, both
// counters must return to zero.
type Tracker struct {
	files atomic.Int64
	iters atomic.Int64
}

// Files reports the number of currently open spill-file handles.
func (t *Tracker) Files() int64 { return t.files.Load() }

// Iters reports the number of currently open merge iterators.
func (t *Tracker) Iters() int64 { return t.iters.Load() }

func (t *Tracker) addFile(delta int64) { t.files.Add(delta) }
func (t *Tracker) addIter(delta int64) { t.iters.Add(delta) }

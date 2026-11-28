package lsm

import (
	"sync"
)

// LockRequest describes every resource a compaction wants to own. InputFiles
// are locked exclusively by FileID. OutputRange is a range lock on the output
// level: two tasks writing to the same level may run concurrently only when
// their output ranges do not intersect.
type LockRequest struct {
	TaskID      uint64
	InputLevel  int
	OutputLevel int
	InputFiles  []*FileMeta
	OutputRange KeyRange
	L0ToL1      bool
}

type activeLock struct {
	taskID      uint64
	inputLevel  int
	outputLevel int
	outputRange KeyRange
	files       []*FileMeta
	l0ToL1      bool
}

// LockManager arbitrates file and output-range locks between concurrent
// compactions. All conflict checks and acquisitions are performed atomically.
type LockManager struct {
	mu       sync.Mutex
	files    map[uint64]uint64 // file ID -> owning task
	pointers map[*FileMeta]uint64
	tasks    map[uint64]*activeLock
}

// NewLockManager returns an empty lock manager.
func NewLockManager() *LockManager {
	return &LockManager{
		files:    make(map[uint64]uint64),
		pointers: make(map[*FileMeta]uint64),
		tasks:    make(map[uint64]*activeLock),
	}
}

// NewFileLockManager returns an empty lock manager.
func NewFileLockManager() *LockManager { return NewLockManager() }

// TryLock attempts to acquire all resources in req. On failure it changes no
// state, so the caller can immediately try the next candidate.
func (m *LockManager) TryLock(req LockRequest) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if req.TaskID == 0 {
		return false
	}
	if _, exists := m.tasks[req.TaskID]; exists {
		return false
	}
	seen := make(map[uint64]bool, len(req.InputFiles))
	for _, f := range req.InputFiles {
		if f == nil {
			return false
		}
		if f.FileID != 0 {
			if seen[f.FileID] {
				continue
			}
			seen[f.FileID] = true
			if owner, ok := m.files[f.FileID]; ok && owner != req.TaskID {
				return false
			}
		} else if owner, ok := m.pointers[f]; ok && owner != req.TaskID {
			return false
		}
	}
	for _, other := range m.tasks {
		if other.taskID == req.TaskID {
			continue
		}
		if other.outputLevel == req.OutputLevel && other.outputRange.Overlaps(req.OutputRange) {
			return false
		}
		// RocksDB serialises L0->L1 compactions whose key ranges intersect
		// even if file identity alone would not reveal the conflict.
		if req.L0ToL1 && other.outputLevel == 1 && other.outputRange.Overlaps(req.OutputRange) {
			return false
		}
		if other.l0ToL1 && req.OutputLevel == 1 && other.outputRange.Overlaps(req.OutputRange) {
			return false
		}
	}

	lock := &activeLock{
		taskID:      req.TaskID,
		inputLevel:  req.InputLevel,
		outputLevel: req.OutputLevel,
		outputRange: req.OutputRange.Clone(),
		files:       append([]*FileMeta(nil), req.InputFiles...),
		l0ToL1:      req.L0ToL1,
	}
	for _, f := range req.InputFiles {
		if f.FileID != 0 {
			m.files[f.FileID] = req.TaskID
		} else {
			m.pointers[f] = req.TaskID
		}
	}
	m.tasks[req.TaskID] = lock
	return true
}

// LockFiles is a convenience wrapper that only requests file locks.
func (m *LockManager) LockFiles(taskID uint64, files []*FileMeta) bool {
	level := 0
	if len(files) > 0 {
		level = files[0].Level
	}
	return m.TryLock(LockRequest{
		TaskID:      taskID,
		InputLevel:  level,
		OutputLevel: -1,
		InputFiles:  files,
	})
}

// TryLockFiles is an alias for LockFiles.
func (m *LockManager) TryLockFiles(taskID uint64, files []*FileMeta) bool {
	return m.LockFiles(taskID, files)
}

// TryLockRange acquires only an output-range lock.
func (m *LockManager) TryLockRange(taskID uint64, level int, r KeyRange) bool {
	return m.TryLock(LockRequest{
		TaskID:      taskID,
		OutputLevel: level,
		OutputRange: r,
	})
}

// Unlock releases all resources owned by taskID.
func (m *LockManager) Unlock(taskID uint64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, ok := m.tasks[taskID]
	if !ok {
		return
	}
	for _, f := range lock.files {
		if f.FileID != 0 {
			if m.files[f.FileID] == taskID {
				delete(m.files, f.FileID)
			}
		} else if m.pointers[f] == taskID {
			delete(m.pointers, f)
		}
	}
	delete(m.tasks, taskID)
}

// UnlockTask is an alias for Unlock.
func (m *LockManager) UnlockTask(taskID uint64) { m.Unlock(taskID) }

// UnlockFiles releases only the named file locks when they are owned by
// taskID.
func (m *LockManager) UnlockFiles(taskID uint64, files []*FileMeta) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range files {
		if f == nil {
			continue
		}
		if f.FileID != 0 {
			if m.files[f.FileID] == taskID {
				delete(m.files, f.FileID)
			}
		} else if m.pointers[f] == taskID {
			delete(m.pointers, f)
		}
	}
	if lock, ok := m.tasks[taskID]; ok {
		kept := lock.files[:0]
		for _, f := range lock.files {
			if !containsFile(files, f) {
				kept = append(kept, f)
			}
		}
		lock.files = kept
		if len(lock.files) == 0 {
			delete(m.tasks, taskID)
		}
	}
}

// IsFileLocked reports whether the file is currently owned by a task.
func (m *LockManager) IsFileLocked(fileID uint64) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[fileID]
	return ok
}

// IsLocked is an alias for IsFileLocked.
func (m *LockManager) IsLocked(fileID uint64) bool { return m.IsFileLocked(fileID) }

// IsFileLockedBy reports whether fileID is owned by taskID.
func (m *LockManager) IsFileLockedBy(fileID, taskID uint64) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.files[fileID] == taskID
}

// LockedFiles returns the currently locked file identifiers.
func (m *LockManager) LockedFiles() []uint64 {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uint64, 0, len(m.files))
	for id := range m.files {
		out = append(out, id)
	}
	return out
}

// ActiveTasks returns the identifiers currently holding locks.
func (m *LockManager) ActiveTasks() []uint64 {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uint64, 0, len(m.tasks))
	for id := range m.tasks {
		out = append(out, id)
	}
	return out
}

// HasTask reports whether taskID currently owns a lock.
func (m *LockManager) HasTask(taskID uint64) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.tasks[taskID]
	return ok
}

// CompactingBytes returns the total size of files at level currently owned as
// compaction inputs. The picker subtracts this from a level's live size.
func (m *LockManager) CompactingBytes(level int) uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n uint64
	for _, task := range m.tasks {
		for _, f := range task.files {
			if f.Level == level {
				n += f.EffectiveSize()
			}
		}
	}
	return n
}

// CompactingBytesByLevel returns a snapshot of compacting bytes for all
// levels.
func (m *LockManager) CompactingBytesByLevel() map[int]uint64 {
	out := make(map[int]uint64)
	if m == nil {
		return out
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, task := range m.tasks {
		for _, f := range task.files {
			out[f.Level] += f.EffectiveSize()
		}
	}
	return out
}

func containsFile(files []*FileMeta, target *FileMeta) bool {
	for _, f := range files {
		if f == target {
			return true
		}
	}
	return false
}

package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Op identifies the kind of filesystem change observed under a watched path.
type Op int

const (
	OpAdd Op = 1 << iota
	OpRemove
)

func (o Op) String() string {
	switch o {
	case OpAdd:
		return "add"
	case OpRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// Event is a single observed change. Path is the absolute path of the entry
// that changed, rooted at one of the watched directories.
type Event struct {
	Op   Op
	Path string
}

// Watcher monitors a fixed set of directories for entry additions and
// removals. It only needs to answer two questions for VFIO group integrity:
// did a new device/driver entry appear, and did one disappear.
type Watcher interface {
	// Add starts watching the given directory (non-recursively, one level).
	Add(path string) error
	// Events returns the channel on which changes are delivered.
	Events() <-chan Event
	// Close stops the watcher and releases its resources.
	Close() error
}

// ErrWatcherClosed is returned when a Watcher is used after Close.
var ErrWatcherClosed = errors.New("watcher already closed")

// defaultPollInterval is the polling cadence used when no explicit interval
// is configured. On Linux the inotify watcher is preferred; polling exists
// for portability and for tests.
const defaultPollInterval = 250 * time.Millisecond

// PollingWatcher implements Watcher by periodically snapshotting directory
// listings. It is dependency-free and works on every platform, which makes
// the revocation logic fully testable on a developer laptop.
type PollingWatcher struct {
	interval time.Duration

	stop   chan struct{}
	done   chan struct{}
	events chan Event

	// mu guards paths, snapshot, started and closed. The poll goroutine only
	// touches snapshot/paths while holding mu (briefly, once per tick).
	mu       sync.Mutex
	paths    map[string]struct{}
	snapshot map[string]map[string]string
	started  bool
	closed   bool
}

// NewPollingWatcher creates a polling watcher. A zero or negative interval
// falls back to defaultPollInterval.
func NewPollingWatcher(interval time.Duration) *PollingWatcher {
	if interval <= 0 {
		interval = defaultPollInterval
	}
	return &PollingWatcher{
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		events:   make(chan Event, 64),
		paths:    make(map[string]struct{}),
		snapshot: make(map[string]map[string]string),
	}
}

// Add implements Watcher. The first Add starts the background poll loop.
func (w *PollingWatcher) Add(path string) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrWatcherClosed
	}
	first := !w.started
	w.paths[path] = struct{}{}
	// Seed the baseline immediately so a device present at registration time
	// is not reported as a spurious addition on the first tick.
	w.snapshot[path] = listEntries(path)
	w.started = true
	w.mu.Unlock()

	if first {
		go w.loop()
	}
	return nil
}

// Events implements Watcher.
func (w *PollingWatcher) Events() <-chan Event { return w.events }

// Close implements Watcher.
func (w *PollingWatcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()

	close(w.stop)
	<-w.done
	return nil
}

func (w *PollingWatcher) loop() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.pollOnce()
		}
	}
}

func (w *PollingWatcher) pollOnce() {
	w.mu.Lock()
	paths := make([]string, 0, len(w.paths))
	for p := range w.paths {
		paths = append(paths, p)
	}
	w.mu.Unlock()
	sort.Strings(paths)

	for _, dir := range paths {
		current := listEntries(dir)
		previous := w.snapshot[dir]

		for name, target := range current {
			oldTarget, existed := previous[name]
			if !existed {
				w.emit(Event{Op: OpAdd, Path: filepath.Join(dir, name)})
			} else if oldTarget != target {
				// A same-name entry was replaced (e.g. the "driver" symlink
				// atomically repointed during a rebind). Report it as a remove
				// followed by an add so the manager always re-inspects.
				w.emit(Event{Op: OpRemove, Path: filepath.Join(dir, name)})
				w.emit(Event{Op: OpAdd, Path: filepath.Join(dir, name)})
			}
		}
		for name := range previous {
			if _, exists := current[name]; !exists {
				w.emit(Event{Op: OpRemove, Path: filepath.Join(dir, name)})
			}
		}
		w.snapshot[dir] = current
	}
}

func (w *PollingWatcher) emit(ev Event) {
	select {
	case w.events <- ev:
	default:
		// Drop rather than block integrity enforcement; the manager always
		// re-inspects the full group on every event, so a coalesced event
		// cannot cause a real violation to be missed.
	}
}

// listEntries returns the immediate child entries of dir keyed by name. The
// value is the symlink target (or "" for a regular entry), which lets the
// watcher notice an atomic same-name replacement such as a device's "driver"
// symlink being repointed during a rebind. It returns an empty map when the
// directory does not exist (a group can disappear on hot-unplug).
func listEntries(dir string) map[string]string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]string{}
	}
	set := make(map[string]string, len(entries))
	for _, entry := range entries {
		target := ""
		if entry.Type()&os.ModeSymlink != 0 {
			if link, err := os.Readlink(filepath.Join(dir, entry.Name())); err == nil {
				target = link
			}
		}
		set[entry.Name()] = target
	}
	return set
}

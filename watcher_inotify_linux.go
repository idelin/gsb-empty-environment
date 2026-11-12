//go:build linux

package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"sync"
	"syscall"
)

// The architectures this proxy supports (linux/amd64, linux/arm64) are both
// little-endian, which is also the byte order of the inotify event payload.
var nativeEndian = binary.LittleEndian

// InotifyWatcher implements Watcher on top of inotify(7). It delivers kernel
// events without the latency of polling and is the default backend on Linux.
type InotifyWatcher struct {
	fd     int
	events chan Event

	mu      sync.Mutex
	watches map[string]int // path -> inotify watch descriptor
	descs   map[int]string // watch descriptor -> path
	closed  bool

	readErr error
	done    chan struct{}
}

// NewInotifyWatcher creates an inotify instance and starts its read loop.
func NewInotifyWatcher() (*InotifyWatcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	w := &InotifyWatcher{
		fd:      fd,
		events:  make(chan Event, 64),
		watches: make(map[string]int),
		descs:   make(map[int]string),
		done:    make(chan struct{}),
	}
	go w.readLoop()
	return w, nil
}

const inotifyMask = syscall.IN_CREATE | syscall.IN_DELETE | syscall.IN_MOVED_TO | syscall.IN_MOVED_FROM

// Add implements Watcher.
func (w *InotifyWatcher) Add(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrWatcherClosed
	}
	if _, ok := w.watches[path]; ok {
		return nil
	}
	wd, err := syscall.InotifyAddWatch(w.fd, path, inotifyMask)
	if err != nil {
		return err
	}
	w.watches[path] = wd
	w.descs[wd] = path
	return nil
}

// Events implements Watcher.
func (w *InotifyWatcher) Events() <-chan Event { return w.events }

// Close implements Watcher.
func (w *InotifyWatcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	fd := w.fd
	w.mu.Unlock()

	err := syscall.Close(fd)
	<-w.done
	return err
}

func (w *InotifyWatcher) readLoop() {
	defer close(w.done)
	buf := make([]byte, 4096)
	for {
		n, err := syscall.Read(w.fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EBADF) {
				return // closed
			}
			w.mu.Lock()
			w.readErr = err
			w.mu.Unlock()
			return
		}
		w.parse(buf[:n])
	}
}

func (w *InotifyWatcher) parse(data []byte) {
	for len(data) >= syscall.SizeofInotifyEvent {
		raw := data[:syscall.SizeofInotifyEvent]
		ev := parseInotifyEvent(raw)
		data = data[syscall.SizeofInotifyEvent:]

		w.mu.Lock()
		dir, ok := w.descs[int(ev.Wd)]
		w.mu.Unlock()
		if !ok || ev.Len == 0 {
			continue
		}
		name := bytesToName(data[:int(ev.Len)])
		data = data[int(ev.Len):]

		if ev.Mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0 {
			w.emit(Event{Op: OpAdd, Path: filepath.Join(dir, name)})
		}
		if ev.Mask&(syscall.IN_DELETE|syscall.IN_MOVED_FROM) != 0 {
			w.emit(Event{Op: OpRemove, Path: filepath.Join(dir, name)})
		}
	}
}

func (w *InotifyWatcher) emit(ev Event) {
	select {
	case w.events <- ev:
	default:
	}
}

// parseInotifyEvent decodes one fixed-size inotify event header.
func parseInotifyEvent(b []byte) syscall.InotifyEvent {
	return syscall.InotifyEvent{
		Wd:     int32(nativeEndian.Uint32(b[0:4])),
		Mask:   nativeEndian.Uint32(b[4:8]),
		Cookie: nativeEndian.Uint32(b[8:12]),
		Len:    nativeEndian.Uint32(b[12:16]),
	}
}

// bytesToName turns the NUL-padded filename payload of an inotify event into
// a clean string.
func bytesToName(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

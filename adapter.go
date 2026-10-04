package waldb

import (
	"errors"
	"io"
	"os"
	"sync"
)

var errCrashed = errors.New("adapter: crashed")

// pendingRun is one not-yet-durable write.
type pendingRun struct {
	off  int64
	data []byte
}

// Adapter is a file adapter with injectable persistence cut points.
//
// Writes go to a volatile in-memory overlay (modelling the OS page
// cache / drive buffers). Only Sync makes them durable in the file.
// Crash discards the overlay and refuses further IO until Restart,
// modelling an abrupt process/machine failure. Reads see the overlay
// merged on top of the durable file, like a running process would.
//
// Truncator, when set, rewrites each run's bytes as they become
// durable, which injects torn/partial flushes (e.g. an incomplete
// tail record or a half-written checkpoint).
type Adapter struct {
	path   string
	region string
	f      *os.File

	mu      sync.Mutex
	pending []pendingRun
	crashed bool

	Truncator func(region string, data []byte) []byte
}

// OpenAdapter opens (or creates) the file at path. region is a label
// ("wal"/"data") passed to the Truncator hook.
func OpenAdapter(path, region string) (*Adapter, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	return &Adapter{path: path, region: region, f: f}, nil
}

// Size returns the durable file size.
func (a *Adapter) Size() (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.crashed {
		return 0, errCrashed
	}
	st, err := a.f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// ReadAt reads the durable file with the volatile overlay applied.
func (a *Adapter) ReadAt(p []byte, off int64) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.crashed {
		return 0, errCrashed
	}
	n, err := a.f.ReadAt(p, off)
	if err != nil && err != io.EOF {
		return n, err
	}
	for _, r := range a.pending {
		for i, b := range r.data {
			pos := r.off + int64(i)
			if pos >= off && pos < off+int64(len(p)) {
				p[pos-off] = b
				if int(pos-off)+1 > n {
					n = int(pos-off) + 1
				}
			}
		}
	}
	return n, nil
}

// WriteAt buffers a write in the volatile overlay; it is not durable
// until Sync.
func (a *Adapter) WriteAt(p []byte, off int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.crashed {
		return errCrashed
	}
	cp := append([]byte(nil), p...)
	a.pending = append(a.pending, pendingRun{off: off, data: cp})
	return nil
}

// Sync applies all pending writes to the file and fsyncs it. This is
// the only durability boundary.
func (a *Adapter) Sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.crashed {
		return errCrashed
	}
	for _, r := range a.pending {
		data := r.data
		if a.Truncator != nil {
			data = a.Truncator(a.region, data)
		}
		if len(data) == 0 {
			continue
		}
		if _, err := a.f.WriteAt(data, r.off); err != nil {
			return err
		}
	}
	if err := a.f.Sync(); err != nil {
		return err
	}
	a.pending = nil
	return nil
}

// WriteSync writes data at off and immediately makes it durable,
// returning the number of bytes that actually became durable. A torn
// flush (injected via Truncator) yields fewer bytes than requested;
// the caller must only advance its durable frontier by the returned
// count and rewrite the remainder later.
func (a *Adapter) WriteSync(off int64, data []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.crashed {
		return 0, errCrashed
	}
	d := data
	if a.Truncator != nil {
		d = a.Truncator(a.region, d)
	}
	if len(d) > 0 {
		if _, err := a.f.WriteAt(d, off); err != nil {
			return 0, err
		}
	}
	if err := a.f.Sync(); err != nil {
		return 0, err
	}
	return len(d), nil
}

// Truncate shrinks the durable file (used to drop a torn log tail).
func (a *Adapter) Truncate(size int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.crashed {
		return errCrashed
	}
	return a.f.Truncate(size)
}

// Crash drops all non-durable writes and fails every subsequent
// operation until Restart.
func (a *Adapter) Crash() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = nil
	a.crashed = true
}

// Restart brings the adapter back after a simulated crash.
func (a *Adapter) Restart() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.crashed = false
}

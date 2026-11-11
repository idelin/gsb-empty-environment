// Package faultfile provides a fault-injecting opener for auditlog tests.
//
// It models the durable state of a file independently of its page cache:
// writes accumulate in memory and only a Sync makes a prefix durable. A Sync
// limit simulates a power loss during flush (torn write), and Crash discards
// every byte that never reached disk.
package faultfile

import (
	"errors"
	"os"
	"sync"

	"example.org/netsec/uid1279/auditlog"
)

// Controller creates fault-injecting files and controls their fault state.
type Controller struct {
	mu sync.Mutex

	syncLimit int64
	oneShot   bool

	crashOnNextSync bool
	last            *FaultFile
}

// Opener adapts the controller to auditlog.Opener.
type Opener struct {
	controller *Controller
}

// New returns a controller and its opener.
func New() (*Controller, *Opener) {
	controller := &Controller{syncLimit: -1}
	return controller, &Opener{controller: controller}
}

// OpenFile implements auditlog.Opener.
func (o *Opener) OpenFile(name string, flag int, perm os.FileMode) (auditlog.File, error) {
	return o.controller.OpenFile(name, flag, perm)
}

// ArmSyncLimit makes the next Sync durable up to durableSize bytes only.
// It models a torn flush at a power-loss boundary.
func (c *Controller) ArmSyncLimit(durableSize int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncLimit = durableSize
	c.oneShot = true
}

// SetSyncLimit constrains every Sync until ClearFaults is called.
func (c *Controller) SetSyncLimit(durableSize int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncLimit = durableSize
	c.oneShot = false
}

// ArmCrashOnNextSync makes the next Sync fail and discard unsynced bytes,
// modelling a sudden power loss mid-flush.
func (c *Controller) ArmCrashOnNextSync() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.crashOnNextSync = true
}

// ClearFaults restores normal durability behavior.
func (c *Controller) ClearFaults() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncLimit = -1
	c.oneShot = false
	c.crashOnNextSync = false
}

// Last returns the most recently opened file.
func (c *Controller) Last() *FaultFile {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// OpenFile opens or creates the backing OS file. Existing data is loaded as
// the durable prefix.
func (c *Controller) OpenFile(name string, flag int, perm os.FileMode) (*FaultFile, error) {
	backing, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	info, err := backing.Stat()
	if err != nil {
		backing.Close()
		return nil, err
	}
	file := &FaultFile{
		controller: c,
		backing:    backing,
		durable:    info.Size(),
		pending:    nil,
	}
	c.mu.Lock()
	c.last = file
	c.mu.Unlock()
	return file, nil
}

// FaultFile is an auditlog.File whose durability can be constrained.
type FaultFile struct {
	controller *Controller
	backing    *os.File

	durable int64
	pending []byte
	closed  bool
}

// Write buffers bytes. Bytes only become durable after Sync.
func (f *FaultFile) Write(data []byte) (int, error) {
	if f.closed {
		return 0, errors.New("faultfile: write on closed file")
	}
	f.pending = append(f.pending, data...)
	return len(data), nil
}

// Sync flushes a durable prefix. With an armed limit, only that many total
// durable bytes survive and the rest stays pending, modelling a torn flush.
func (f *FaultFile) Sync() error {
	f.controller.mu.Lock()
	limit := f.controller.syncLimit
	oneShot := f.controller.oneShot
	crash := f.controller.crashOnNextSync
	if oneShot || crash {
		f.controller.syncLimit = -1
		f.controller.oneShot = false
		f.controller.crashOnNextSync = false
	}
	f.controller.mu.Unlock()

	if crash {
		f.pending = nil
		return errors.New("faultfile: simulated power loss during sync")
	}

	want := f.durable + int64(len(f.pending))
	target := want
	if limit >= 0 && limit < target {
		target = limit
	}
	if target < f.durable {
		return errors.New("faultfile: sync limit below already durable size")
	}

	flushBytes := target - f.durable
	if flushBytes > 0 {
		if _, err := f.backing.WriteAt(f.pending[:flushBytes], f.durable); err != nil {
			return err
		}
		if err := f.backing.Sync(); err != nil {
			return err
		}
	}
	f.pending = f.pending[flushBytes:]
	f.durable = target

	if limit >= 0 && target < want {
		return errors.New("faultfile: simulated torn sync (partial durability)")
	}
	return nil
}

// Close syncs remaining buffered bytes and closes the backing file.
func (f *FaultFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	if len(f.pending) > 0 {
		if _, err := f.backing.WriteAt(f.pending, f.durable); err != nil {
			f.backing.Close()
			return err
		}
		f.durable += int64(len(f.pending))
		f.pending = nil
	}
	return f.backing.Close()
}

// Crash discards every byte that never reached disk and closes the file, as
// if power was cut. Reopening the path later shows only the durable prefix.
func (f *FaultFile) Crash() error {
	if f.closed {
		return nil
	}
	f.closed = true
	f.pending = nil
	if err := f.backing.Truncate(f.durable); err != nil {
		f.backing.Close()
		return err
	}
	return f.backing.Close()
}

// Name returns the backing file path.
func (f *FaultFile) Name() string {
	return f.backing.Name()
}

// Stat reports the durable file size, not the in-memory buffered length.
func (f *FaultFile) Stat() (os.FileInfo, error) {
	return f.backing.Stat()
}

// DurableSize reports how many bytes have survived a Sync.
func (f *FaultFile) DurableSize() int64 {
	return f.durable
}

// PendingSize reports buffered bytes that are not yet durable.
func (f *FaultFile) PendingSize() int64 {
	return int64(len(f.pending))
}

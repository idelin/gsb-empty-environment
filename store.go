package snaprecv

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// On-disk layout under the root dir:
//
//	staging/data            staged bytes for the in-progress version
//	staging/manifest.json   fsynced record of verified intervals
//	snapshots/v<N>/data     fully verified, installable snapshots
//	current -> snapshots/v<N>   atomically swapped visible state
//
// Write protocol per chunk: pwrite data, fsync data, then fsync the
// manifest. A crash anywhere leaves either the old manifest (chunk not
// verified, will be redelivered) or the new one (chunk reusable).

// fileWriter abstracts the staging data file.
type fileWriter interface {
	WriteAt(p []byte, off int64) (int, error)
	ReadAt(p []byte, off int64) (int, error)
	Sync() error
	Close() error
}

type interval struct {
	Off uint64 `json:"off"`
	Len uint64 `json:"len"`
}

type manifest struct {
	Version   uint64     `json:"version"`
	TotalSize uint64     `json:"total_size"`
	Intervals []interval `json:"intervals"`
}

func (r *Receiver) stagingDir() string   { return filepath.Join(r.dir, "staging") }
func (r *Receiver) snapshotsDir() string { return filepath.Join(r.dir, "snapshots") }
func (r *Receiver) snapshotDir(v uint64) string {
	return filepath.Join(r.snapshotsDir(), fmt.Sprintf("v%d", v))
}
func (r *Receiver) currentPath() string     { return filepath.Join(r.dir, "current") }
func (r *Receiver) currentDataPath() string { return filepath.Join(r.currentPath(), "data") }
func (r *Receiver) manifestPath() string    { return filepath.Join(r.stagingDir(), "manifest.json") }

// resetStaging discards any staged state and starts staging version v.
func (r *Receiver) resetStaging(v uint64) error {
	if r.data != nil {
		r.data.Close()
		r.data = nil
	}
	if err := os.RemoveAll(r.stagingDir()); err != nil {
		return err
	}
	if err := os.MkdirAll(r.stagingDir(), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(r.stagingDir(), "data"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	r.data = f
	r.staged = v
	r.total = 0
	r.intervals = nil
	// Persist the staged version immediately so recovery sees the same
	// staging state even if the first chunk for v is rejected afterwards.
	return r.writeManifest()
}

// writeManifest atomically persists the verified-interval manifest.
func (r *Receiver) writeManifest() error {
	m := manifest{Version: r.staged, TotalSize: r.total, Intervals: r.intervals}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := r.manifestPath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.manifestPath()); err != nil {
		return err
	}
	return syncDir(r.stagingDir())
}

// install atomically makes the fully verified staged version visible.
func (r *Receiver) install() error {
	version := r.staged

	r.data.Sync()
	r.data.Close()
	r.data = nil

	dst := r.snapshotDir(version)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.Rename(r.stagingDir(), dst); err != nil {
		return err
	}
	if err := syncDir(r.snapshotsDir()); err != nil {
		return err
	}
	r.call(HookInstallRenamed)

	if err := r.swapCurrent(version); err != nil {
		return err
	}
	r.call(HookInstallSymlinked)

	r.installed = version
	r.staged = 0
	r.total = 0
	r.intervals = nil

	r.cleanupSnapshots()
	r.call(HookInstallCleaned)
	return nil
}

// swapCurrent atomically repoints `current` at snapshots/v<N> using a
// rename, which is atomic on POSIX filesystems.
func (r *Receiver) swapCurrent(version uint64) error {
	tmp := filepath.Join(r.dir, "current.tmp")
	os.Remove(tmp)
	target := filepath.Join("snapshots", fmt.Sprintf("v%d", version))
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.currentPath()); err != nil {
		return err
	}
	return syncDir(r.dir)
}

// cleanupSnapshots removes snapshots older than the installed version.
func (r *Receiver) cleanupSnapshots() {
	for _, v := range r.snapshotVersions() {
		if v < r.installed {
			os.RemoveAll(r.snapshotDir(v))
		}
	}
}

func (r *Receiver) snapshotVersions() []uint64 {
	entries, err := ioutil.ReadDir(r.snapshotsDir())
	if err != nil {
		return nil
	}
	var out []uint64
	for _, e := range entries {
		if v, ok := parseVersionDir(e.Name()); ok {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func parseVersionDir(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "v") {
		return 0, false
	}
	v, err := strconv.ParseUint(name[1:], 10, 64)
	return v, err == nil
}

// recover restores durable state after a (possibly unclean) shutdown.
func (r *Receiver) recover() error {
	if err := os.MkdirAll(r.snapshotsDir(), 0755); err != nil {
		return err
	}

	// Installed version comes from the `current` symlink.
	if target, err := os.Readlink(r.currentPath()); err == nil {
		if v, ok := parseVersionDir(filepath.Base(target)); ok {
			r.installed = v
		}
	}

	// A crash between the rename and symlink cut points leaves a fully
	// verified snapshot that was never made visible; finishing the swap
	// is idempotent, so complete it now.
	for _, v := range r.snapshotVersions() {
		if v > r.installed {
			if err := r.swapCurrent(v); err != nil {
				return err
			}
			r.installed = v
		}
	}
	r.cleanupSnapshots()

	// Reload staged progress; verified chunks are reused as-is.
	m, err := r.readManifest()
	if err != nil {
		return err
	}
	if m == nil || m.Version <= r.installed {
		// No staging in progress, or staging for a version that can
		// never be installed anymore.
		os.RemoveAll(r.stagingDir())
		return nil
	}
	f, err := os.OpenFile(filepath.Join(r.stagingDir(), "data"), os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	r.data = f
	r.staged = m.Version
	r.total = m.TotalSize
	r.intervals = m.Intervals
	// A crash between the final manifest sync and install left a fully
	// verified staging area; finishing the install is idempotent.
	if r.covered() {
		return r.install()
	}
	return nil
}

func (r *Receiver) readManifest() (*manifest, error) {
	b, err := readFile(r.manifestPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		// A torn manifest write is impossible by protocol (tmp+rename),
		// but treat corruption as "no usable staging" rather than fatal.
		return nil, nil
	}
	return &m, nil
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func readFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

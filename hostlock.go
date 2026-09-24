// Package hostlock implements a same-host, multi-process mutual-exclusion
// protocol with lease-based owner records and monotonic fencing tokens.
//
// All state lives in a caller-specified directory:
//
//	<dir>/<name>.mutex   serialization file (flock), never holds data
//	<dir>/<name>.owner   JSON owner record, written atomically (tmp+rename)
//	<dir>/<name>.token   last issued fencing token, monotonic, fsynced
//
// Ownership safety never depends on PID liveness: a lease expiry (wall
// clock) plus a per-acquisition random nonce and a monotonic fencing token
// decide everything. PID probing only feeds the advisory Live/Lost status.
package hostlock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

var (
	// ErrHeld is returned by TryAcquire when a valid lease is held.
	ErrHeld = errors.New("hostlock: lock is held")
	// ErrNotHeld is returned when the lock has no owner record.
	ErrNotHeld = errors.New("hostlock: lock is not held")
	// ErrNotOwner is returned when the caller is not the recorded owner,
	// e.g. when releasing or renewing someone else's lock.
	ErrNotOwner = errors.New("hostlock: not the lock owner")
	// ErrLeaseExpired is returned by Renew when the lease already lapsed;
	// the lock must be re-acquired (which issues a new fencing token).
	ErrLeaseExpired = errors.New("hostlock: lease expired")
)

// HeldError describes the current owner when TryAcquire fails with ErrHeld.
type HeldError struct {
	Owner OwnerRecord
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("%s (pid %d on %s, token %d, expires %s)",
		ErrHeld, e.Owner.PID, e.Owner.Host, e.Owner.Token,
		e.Owner.ExpiresAt.Format(time.RFC3339Nano))
}

// Unwrap returns ErrHeld so errors.Is(err, ErrHeld) matches.
func (e *HeldError) Unwrap() error { return ErrHeld }

// Status classifies the lock as seen by an observer.
type Status int

const (
	// StatusFree means no owner record exists; the lock can be acquired.
	StatusFree Status = iota
	// StatusLive means a valid lease is held and the owner process appears alive.
	StatusLive
	// StatusLost means a valid lease is held but the owner process is gone
	// (crashed without releasing). Others must wait out the lease.
	StatusLost
	// StatusStale means the lease has expired; the lock may be taken over.
	StatusStale
)

func (s Status) String() string {
	switch s {
	case StatusFree:
		return "free"
	case StatusLive:
		return "live"
	case StatusLost:
		return "lost"
	case StatusStale:
		return "stale"
	}
	return "unknown"
}

// Hooks are fault-injection points. They are nil in production.
type Hooks struct {
	// FailBeforeOwnerWrite runs after the fencing token is persisted but
	// before the owner record is written; a non-nil result aborts the
	// acquire, simulating a crash in that window.
	FailBeforeOwnerWrite func() error
	// FailBeforeFsync runs before every fsync; a non-nil result aborts the
	// write, simulating fsync failure / power loss.
	FailBeforeFsync func(path string) error
}

// Options configures a Lock.
type Options struct {
	Dir    string        // caller-specified directory holding all lock files
	Name   string        // lock name: [A-Za-z0-9._-], used as file prefix
	Lease  time.Duration // lease duration for each acquire/renew
	Clock  Clock         // defaults to RealClock
	Prober Prober        // defaults to SystemProber
	Hooks  *Hooks        // fault injection, nil in production
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// OwnerRecord is the on-disk, JSON-encoded description of the lock holder.
type OwnerRecord struct {
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	PID        int       `json:"pid"`
	Host       string    `json:"host"`
	Nonce      string    `json:"nonce"` // secret per acquisition; required to renew/release
	StartSig   string    `json:"start_sig,omitempty"`
	Token      uint64    `json:"token"` // fencing token, strictly increasing per acquisition
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Lock is a client of the named mutex. Multiple Lock values (in this or
// other processes) may point at the same dir/name; they coordinate through
// the filesystem.
type Lock struct {
	dir    string
	name   string
	lease  time.Duration
	clock  Clock
	prober Prober
	hooks  *Hooks
}

// New creates a Lock, creating Dir if needed.
func New(opts Options) (*Lock, error) {
	if opts.Dir == "" {
		return nil, errors.New("hostlock: Dir is required")
	}
	if !nameRe.MatchString(opts.Name) {
		return nil, fmt.Errorf("hostlock: invalid lock name %q", opts.Name)
	}
	if opts.Lease <= 0 {
		return nil, errors.New("hostlock: Lease must be positive")
	}
	if opts.Clock == nil {
		opts.Clock = RealClock{}
	}
	if opts.Prober == nil {
		opts.Prober = SystemProber{}
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}
	l := &Lock{dir: opts.Dir, name: opts.Name, lease: opts.Lease, clock: opts.Clock, prober: opts.Prober, hooks: opts.Hooks}
	return l, nil
}

func (l *Lock) mutexPath() string { return filepath.Join(l.dir, l.name+".mutex") }
func (l *Lock) ownerPath() string { return filepath.Join(l.dir, l.name+".owner") }
func (l *Lock) tokenPath() string { return filepath.Join(l.dir, l.name+".token") }

// Handle proves ownership of one acquisition. It is only produced by
// Acquire/TryAcquire; the nonce inside it is never written to disk in a
// guessable way, so another process (including one that later reuses the
// same PID) cannot forge it.
type Handle struct {
	lock      *Lock
	nonce     string
	token     uint64
	expiresAt time.Time
}

// Token returns the fencing token issued for this acquisition.
func (h *Handle) Token() uint64 { return h.token }

// ExpiresAt returns the current lease expiry.
func (h *Handle) ExpiresAt() time.Time { return h.expiresAt }

// TryAcquire attempts to take the lock once. It succeeds when the lock is
// free or the current lease is stale (expired), issuing a new fencing token.
// Otherwise it returns a *HeldError wrapping ErrHeld.
func (l *Lock) TryAcquire() (*Handle, error) {
	unlock, err := l.lockMutex()
	if err != nil {
		return nil, err
	}
	defer unlock()

	rec, err := l.readOwner()
	if err != nil {
		return nil, err
	}
	now := l.clock.Now()
	if rec != nil && now.Before(rec.ExpiresAt) {
		return nil, &HeldError{Owner: *rec}
	}

	token, err := l.nextToken()
	if err != nil {
		return nil, err
	}
	if l.hooks != nil && l.hooks.FailBeforeOwnerWrite != nil {
		if err := l.hooks.FailBeforeOwnerWrite(); err != nil {
			return nil, err
		}
	}
	host, _ := os.Hostname()
	_, sig := l.prober.Probe(os.Getpid())
	newRec := &OwnerRecord{
		Version:    1,
		Name:       l.name,
		PID:        os.Getpid(),
		Host:       host,
		Nonce:      randomNonce(),
		StartSig:   sig,
		Token:      token,
		AcquiredAt: now,
		ExpiresAt:  now.Add(l.lease),
	}
	if err := l.writeOwner(newRec); err != nil {
		return nil, err
	}
	return &Handle{lock: l, nonce: newRec.Nonce, token: token, expiresAt: newRec.ExpiresAt}, nil
}

// Acquire retries TryAcquire until it succeeds or ctx is canceled. It polls
// with real-time sleeps of lease/4, capped at 50ms.
func (l *Lock) Acquire(ctx context.Context) (*Handle, error) {
	interval := l.lease / 4
	if interval <= 0 || interval > 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	for {
		h, err := l.TryAcquire()
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, ErrHeld) {
			return nil, err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Inspect reads the current owner record and classifies it.
func (l *Lock) Inspect() (rec *OwnerRecord, status Status, err error) {
	rec, err = l.readOwner()
	if err != nil || rec == nil {
		return rec, StatusFree, err
	}
	now := l.clock.Now()
	if !now.Before(rec.ExpiresAt) {
		return rec, StatusStale, nil
	}
	alive, sig := l.prober.Probe(rec.PID)
	if !alive {
		return rec, StatusLost, nil
	}
	// PID reuse guard: same PID but a different process instance.
	if rec.StartSig != "" && sig != "" && sig != rec.StartSig {
		return rec, StatusLost, nil
	}
	return rec, StatusLive, nil
}

// Renew extends the lease. It fails with ErrNotOwner if the record no longer
// belongs to this handle, and with ErrLeaseExpired if the lease has lapsed
// (in which case a takeover may already be underway; re-acquire instead).
func (h *Handle) Renew() error {
	l := h.lock
	unlock, err := l.lockMutex()
	if err != nil {
		return err
	}
	defer unlock()

	rec, err := l.readOwner()
	if err != nil {
		return err
	}
	if rec == nil {
		return ErrNotHeld
	}
	if rec.Nonce != h.nonce {
		return ErrNotOwner
	}
	now := l.clock.Now()
	if !now.Before(rec.ExpiresAt) {
		return ErrLeaseExpired
	}
	rec.ExpiresAt = now.Add(l.lease)
	if err := l.writeOwner(rec); err != nil {
		return err
	}
	h.expiresAt = rec.ExpiresAt
	return nil
}

// Release deletes the owner record, but only if it still belongs to this
// handle. Releasing someone else's lock is refused with ErrNotOwner and
// leaves the record untouched.
func (h *Handle) Release() error {
	l := h.lock
	unlock, err := l.lockMutex()
	if err != nil {
		return err
	}
	defer unlock()

	rec, err := l.readOwner()
	if err != nil {
		return err
	}
	if rec == nil {
		return ErrNotHeld
	}
	if rec.Nonce != h.nonce {
		return ErrNotOwner
	}
	if err := os.Remove(l.ownerPath()); err != nil {
		return err
	}
	return fsyncDir(l.dir, l.fsyncHook())
}

// lockMutex serializes all mutations of owner/token files on this host via
// flock(2). The kernel releases the flock if the holder dies, so a crash
// mid-mutation cannot deadlock the lock.
func (l *Lock) lockMutex() (unlock func(), err error) {
	f, err := os.OpenFile(l.mutexPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// readOwner reads the owner record. Reads need no flock: the file is
// replaced atomically by rename, so a reader sees either the old or the new
// record, never a torn one.
func (l *Lock) readOwner() (*OwnerRecord, error) {
	data, err := os.ReadFile(l.ownerPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec OwnerRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("hostlock: corrupt owner record: %w", err)
	}
	return &rec, nil
}

func (l *Lock) writeOwner(rec *OwnerRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return l.writeFileAtomic(l.ownerPath(), data)
}

// nextToken reads, increments and persists the fencing token. The token file
// is never deleted, so tokens stay monotonic across releases, crashes and
// reboots.
func (l *Lock) nextToken() (uint64, error) {
	var cur uint64
	data, err := os.ReadFile(l.tokenPath())
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return 0, err
	default:
		if _, err := fmt.Sscanf(string(data), "%d", &cur); err != nil {
			return 0, fmt.Errorf("hostlock: corrupt token file: %w", err)
		}
	}
	next := cur + 1
	if err := l.writeFileAtomic(l.tokenPath(), []byte(fmt.Sprintf("%d\n", next))); err != nil {
		return 0, err
	}
	return next, nil
}

// writeFileAtomic writes data to a temp file, fsyncs it, renames it over
// path, then fsyncs the directory. After it returns nil, the contents are
// durable on any storage stack that honors fsync.
func (l *Lock) writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp-" + randomNonce()
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if l.hooks != nil && l.hooks.FailBeforeFsync != nil {
		if err := l.hooks.FailBeforeFsync(tmp); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return fsyncDir(l.dir, l.fsyncHook())
}

func (l *Lock) fsyncHook() func(string) error {
	if l.hooks == nil {
		return nil
	}
	return l.hooks.FailBeforeFsync
}

func fsyncDir(dir string, hook func(string) error) error {
	if hook != nil {
		if err := hook(dir); err != nil {
			return err
		}
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func randomNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

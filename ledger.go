// Package netsec implements a session ledger for a security-device protocol
// in which confirmation messages may be retransmitted, but the capability to
// execute an operation can be consumed exactly once.
//
// Each operation ID is bound to the session generation and the digest of the
// request that first consumed it. The side effect and its consume receipt are
// committed to disk atomically (write-temp + fsync + rename + fsync dir), so
// a retransmission of identical content returns the original result without
// re-executing the side effect, while conflicting content is rejected.
//
// Crash model: an intent record is persisted before the side effect runs.
// If power is lost between the intent and the commit, recovery treats the
// capability as spent (fail-closed) and reports ErrInterrupted instead of
// re-executing. Retransmissions therefore never trigger an operation twice,
// including in a later session after reconnect.
package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Receipt states persisted on disk.
const (
	statePrepared  = "prepared"  // intent durable, side effect not yet committed
	stateCommitted = "committed" // side effect committed, result durable
)

var (
	// ErrSessionExpired is returned when the confirmation carries a session
	// generation that is not the ledger's current generation, or when the
	// operation was already consumed under an older (expired) generation.
	ErrSessionExpired = errors.New("netsec: session generation expired or unknown")
	// ErrConflict is returned when an operation ID is retransmitted with a
	// request digest different from the one that consumed it.
	ErrConflict = errors.New("netsec: conflicting request for consumed operation")
	// ErrInterrupted is returned when a previous attempt was interrupted
	// (e.g. power loss) after the intent was persisted but before the commit
	// landed. The capability is consumed; the side effect is not retried.
	ErrInterrupted = errors.New("netsec: operation interrupted before commit; capability consumed")
)

// ApplyFunc performs the host side effect and returns its result payload.
// It is invoked at most once per operation ID across the lifetime of the
// ledger directory, including across crashes and session reconnects.
type ApplyFunc func() (result []byte, err error)

// Receipt is the durable record of a consumed operation.
type Receipt struct {
	OpID   string `json:"op_id"`
	Gen    uint64 `json:"gen"`
	Digest string `json:"digest"` // hex SHA-256 of the request
	State  string `json:"state"`  // prepared | committed
	Result []byte `json:"result"` // side-effect result payload
	// ExecErr records a side-effect failure. The capability is still
	// consumed (fail-closed); retransmissions replay the same failure.
	ExecErr string `json:"exec_err,omitempty"`
	// Replayed is true when the receipt was served from the ledger instead
	// of executing the side effect. It is not persisted.
	Replayed bool `json:"-"`
}

type sessionFile struct {
	CurrentGen uint64 `json:"current_gen"`
}

// Ledger is a durable, goroutine-safe session ledger rooted at a directory.
type Ledger struct {
	dir      string
	mu       sync.Mutex
	gen      uint64
	receipts map[string]*Receipt

	// crashAfterIntent, when non-nil, runs after the intent record is
	// durable and before the side effect executes. It simulates power loss
	// in tests.
	crashAfterIntent func()
}

// Open loads (or initializes) the ledger rooted at dir.
func Open(dir string) (*Ledger, error) {
	l := &Ledger{dir: dir, gen: 1, receipts: make(map[string]*Receipt)}
	if err := os.MkdirAll(l.receiptDir(), 0o755); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(l.sessionPath())
	switch {
	case err == nil:
		var sf sessionFile
		if err := json.Unmarshal(raw, &sf); err != nil {
			return nil, fmt.Errorf("netsec: corrupt session file: %w", err)
		}
		if sf.CurrentGen > 0 {
			l.gen = sf.CurrentGen
		}
	case errors.Is(err, os.ErrNotExist):
		if err := l.persistSession(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	entries, err := os.ReadDir(l.receiptDir())
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".json.tmp") {
			continue // orphan temp files from a crash mid-rename are ignored
		}
		raw, err := os.ReadFile(filepath.Join(l.receiptDir(), name))
		if err != nil {
			return nil, err
		}
		var r Receipt
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("netsec: corrupt receipt %s: %w", name, err)
		}
		rc := r
		l.receipts[r.OpID] = &rc
	}
	return l, nil
}

// CurrentGen returns the current session generation.
func (l *Ledger) CurrentGen() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gen
}

// AdvanceGen starts a new session (e.g. after reconnect) and invalidates the
// previous generation. The new generation is persisted atomically before
// returning, so confirmations carrying an older generation are rejected even
// after a restart.
func (l *Ledger) AdvanceGen() (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gen++
	if err := l.persistSession(); err != nil {
		return 0, err
	}
	return l.gen, nil
}

// Confirm consumes the capability bound to opID exactly once.
//
//   - gen must equal the current session generation, else ErrSessionExpired.
//   - A retransmission identical to the committed request returns the stored
//     receipt (Replayed=true) without re-running apply.
//   - Same opID with different request content returns ErrConflict.
//   - An opID consumed under an expired generation returns ErrSessionExpired.
//   - An opID interrupted before commit returns ErrInterrupted.
//
// Otherwise the intent is persisted, apply runs once, and the receipt with
// its result is committed atomically before Confirm returns.
func (l *Ledger) Confirm(gen uint64, opID string, request []byte, apply ApplyFunc) (*Receipt, error) {
	if err := validateOpID(opID); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(request)

	l.mu.Lock()
	defer l.mu.Unlock()

	if gen != l.gen {
		return nil, ErrSessionExpired
	}
	if r, ok := l.receipts[opID]; ok {
		if r.Gen != gen {
			return nil, ErrSessionExpired
		}
		if r.Digest != hex.EncodeToString(digest[:]) {
			return nil, ErrConflict
		}
		if r.State == statePrepared {
			return nil, ErrInterrupted
		}
		out := *r
		out.Replayed = true
		return &out, nil
	}

	rec := &Receipt{
		OpID:   opID,
		Gen:    gen,
		Digest: hex.EncodeToString(digest[:]),
		State:  statePrepared,
	}
	if err := l.persistReceipt(rec); err != nil {
		return nil, err
	}
	l.receipts[opID] = rec // capability consumed in-memory from this point

	if l.crashAfterIntent != nil {
		l.crashAfterIntent()
	}

	result, err := apply()
	if err != nil {
		rec.ExecErr = err.Error()
	} else {
		rec.Result = result
	}
	rec.State = stateCommitted
	if err := l.persistReceipt(rec); err != nil {
		return nil, err
	}
	out := *rec
	return &out, nil
}

// Receipts returns the committed and prepared receipts, sorted by op ID.
func (l *Ledger) Receipts() []Receipt {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Receipt, 0, len(l.receipts))
	for _, r := range l.receipts {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OpID < out[j].OpID })
	return out
}

func (l *Ledger) receiptDir() string  { return filepath.Join(l.dir, "receipts") }
func (l *Ledger) sessionPath() string { return filepath.Join(l.dir, "session.json") }
func (l *Ledger) receiptPath(opID string) string {
	return filepath.Join(l.receiptDir(), opID+".json")
}

func (l *Ledger) persistSession() error {
	raw, err := json.Marshal(sessionFile{CurrentGen: l.gen})
	if err != nil {
		return err
	}
	return atomicWrite(l.sessionPath(), raw)
}

func (l *Ledger) persistReceipt(r *Receipt) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicWrite(l.receiptPath(r.OpID), raw)
}

// atomicWrite durably replaces path with data: write temp file in the same
// directory, fsync, rename over the target, then fsync the directory so the
// rename itself survives a crash.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func validateOpID(opID string) error {
	if opID == "" || len(opID) > 128 {
		return fmt.Errorf("netsec: invalid operation ID %q", opID)
	}
	for _, c := range opID {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.'
		if !ok {
			return fmt.Errorf("netsec: invalid operation ID %q", opID)
		}
	}
	return nil
}

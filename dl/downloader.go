// Package dl implements a parallel ranged-download library against a local
// mock origin. Responses carry the entity version (ETag) and the actual
// range returned; a range is confirmed only after it is fully written to
// disk and its digest verifies. Overlapping responses merge only when
// their bytes agree, a version change invalidates the whole round, and
// checkpoints are saved atomically and re-verified on restart.
package dl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// ErrInconsistentOverlap is fatal: two responses of the same entity
// version disagree on the bytes of an overlapping, already confirmed
// range, so the origin data cannot be trusted.
var ErrInconsistentOverlap = errors.New("dl: inconsistent overlapping response")

var (
	errRoundInvalid = errors.New("dl: entity version changed, round invalidated")
	errShortWrite   = errors.New("dl: short write, body shorter than declared range")
)

// Downloader fetches an entity from Server into destPath, resuming from
// ckptPath. It is single-use; after a crash construct a fresh instance.
type Downloader struct {
	server     *Server
	destPath   string
	ckptPath   string
	workers    int
	maxRetries int
	faults     *Faults

	mu            sync.Mutex
	manifest      Manifest
	file          *os.File
	confirmed     []bool
	confirmedCnt  int
	roundInvalid  bool
	fatalErr      error
	ckptSaveCount int
}

// NewDownloader builds a downloader writing destPath with checkpoints in
// ckptPath. faults may be nil.
func NewDownloader(s *Server, destPath, ckptPath string, workers int, faults *Faults) *Downloader {
	if workers < 1 {
		workers = 1
	}
	if faults == nil {
		faults = &Faults{}
	}
	return &Downloader{
		server:     s,
		destPath:   destPath,
		ckptPath:   ckptPath,
		workers:    workers,
		maxRetries: 10,
		faults:     faults,
	}
}

// Run downloads until the whole entity is confirmed. It returns ErrCrash
// when a fault-injected crash fires; construct a new Downloader to resume.
func (d *Downloader) Run(ctx context.Context) error {
	for {
		d.manifest = d.server.Manifest()
		d.confirmed = make([]bool, len(d.manifest.Hashes))
		d.confirmedCnt = 0
		d.roundInvalid = false
		d.fatalErr = nil

		f, err := os.OpenFile(d.destPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return err
		}
		d.file = f
		d.loadCheckpoint()
		if d.confirmedCnt == len(d.confirmed) {
			d.file.Close()
			return nil
		}
		err = d.runRound(ctx)
		d.file.Close()
		if err == errRoundInvalid {
			continue
		}
		return err
	}
}

// runRound fetches all unconfirmed chunks with a worker pool.
func (d *Downloader) runRound(ctx context.Context) error {
	pending := make([]int, 0, len(d.confirmed))
	for i := range d.confirmed {
		if !d.confirmed[i] {
			pending = append(pending, i)
		}
	}
	var next int64
	var wg sync.WaitGroup
	for w := 0; w < d.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if d.aborted() {
					return
				}
				i := int(atomic.AddInt64(&next, 1)) - 1
				if i >= len(pending) {
					return
				}
				err := d.fetchChunk(ctx, pending[i])
				if err == nil {
					continue
				}
				if err == errRoundInvalid {
					return
				}
				d.setFatal(err)
				return
			}
		}()
	}
	wg.Wait()

	if d.fatalErr != nil {
		return d.fatalErr
	}
	if d.roundInvalid {
		return errRoundInvalid
	}
	if d.confirmedCnt != len(d.confirmed) {
		return fmt.Errorf("dl: round finished with %d/%d chunks confirmed", d.confirmedCnt, len(d.confirmed))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.file.Truncate(d.manifest.Size); err != nil {
		return err
	}
	if err := d.file.Sync(); err != nil {
		return err
	}
	return d.saveCheckpointLocked()
}

// fetchChunk fetches one chunk, retrying short writes and unconfirmed
// results up to maxRetries.
func (d *Downloader) fetchChunk(ctx context.Context, idx int) error {
	if d.isConfirmed(idx) {
		return nil
	}
	start, end := d.chunkBounds(idx)
	for attempt := 0; attempt <= d.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.aborted() {
			return errRoundInvalid
		}
		short := false
		for _, r := range d.server.Fetch(start, end) {
			err := d.handleResponse(r)
			if err == errShortWrite {
				short = true
				break
			}
			if err != nil {
				return err
			}
		}
		if !short && d.isConfirmed(idx) {
			return nil
		}
	}
	return fmt.Errorf("dl: chunk %d not confirmed after %d retries", idx, d.maxRetries)
}

// handleResponse validates one response and merges it into the download
// state. Caller does not need to hold the lock.
func (d *Downloader) handleResponse(r Response) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if r.ETag != d.manifest.ETag {
		// Revalidate against the origin: if the origin now serves the
		// response's version, the entity changed and the whole round is
		// invalidated; otherwise the response is a stale late arrival.
		if cur := d.server.Manifest(); cur.ETag == r.ETag {
			d.roundInvalid = true
			os.Remove(d.ckptPath)
			return errRoundInvalid
		}
		return nil
	}

	if int64(len(r.Data)) != r.DeclaredEnd-r.Start+1 {
		return errShortWrite
	}

	actualEnd := r.Start + int64(len(r.Data)) - 1
	first := int(r.Start / d.manifest.ChunkSize)
	last := int(actualEnd / d.manifest.ChunkSize)

	// Before writing anything, every confirmed chunk the response
	// overlaps must agree byte-for-byte; never clobber confirmed data
	// with unverified bytes.
	for idx := first; idx <= last && idx < len(d.confirmed); idx++ {
		if !d.confirmed[idx] {
			continue
		}
		cs, ce := d.chunkBounds(idx)
		os_, oe := max64(cs, r.Start), min64(ce, actualEnd)
		if os_ > oe {
			continue
		}
		buf := make([]byte, oe-os_+1)
		if _, err := d.file.ReadAt(buf, os_); err != nil {
			return err
		}
		if !bytes.Equal(buf, r.Data[os_-r.Start:oe-r.Start+1]) {
			return ErrInconsistentOverlap
		}
	}

	if n, err := d.file.WriteAt(r.Data, r.Start); err != nil {
		return err
	} else if n != len(r.Data) {
		return io.ErrShortWrite
	}

	// Confirm unconfirmed chunks the response fully covers, but only
	// after the on-disk bytes verify against the manifest.
	for idx := first; idx <= last && idx < len(d.confirmed); idx++ {
		cs, ce := d.chunkBounds(idx)
		if d.confirmed[idx] || r.Start > cs || actualEnd < ce {
			continue
		}
		if d.verifyChunk(idx) {
			d.confirmed[idx] = true
			d.confirmedCnt++
			if err := d.saveCheckpointLocked(); err != nil {
				return err
			}
		}
	}
	return nil
}

// verifyChunk re-reads chunk idx from disk and checks its digest against
// the manifest. Caller must hold d.mu or be single-threaded.
func (d *Downloader) verifyChunk(idx int) bool {
	cs, ce := d.chunkBounds(idx)
	buf := make([]byte, ce-cs+1)
	if _, err := d.file.ReadAt(buf, cs); err != nil {
		return false
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]) == d.manifest.Hashes[idx]
}

func (d *Downloader) chunkBounds(idx int) (int64, int64) {
	start := int64(idx) * d.manifest.ChunkSize
	end := start + d.manifest.ChunkSize - 1
	if end > d.manifest.Size-1 {
		end = d.manifest.Size - 1
	}
	return start, end
}

func (d *Downloader) isConfirmed(idx int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.confirmed[idx]
}

func (d *Downloader) aborted() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.roundInvalid || d.fatalErr != nil
}

func (d *Downloader) setFatal(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fatalErr == nil {
		d.fatalErr = err
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

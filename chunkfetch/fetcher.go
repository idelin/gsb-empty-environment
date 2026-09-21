package chunkfetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// Per-chunk digest mismatch: the chunk itself is bad even though the frame was
// well-formed. It is retriable; only the affected chunk is re-fetched.
var ErrChunkDigest = errors.New("chunkfetch: chunk digest mismatch")

const (
	dirChunks    = "chunks"
	fileOutput   = "object.bin"
	fileProgress = "progress.json"
	fileMarker   = "COMPLETE"
)

// Config configures a Fetcher.
type Config struct {
	// Dir is the working directory holding chunk parts, bitmap, output and
	// completion marker.
	Dir string
	// Attempts is the maximum number of tries per chunk (>=1).
	Attempts int
	// InitialBackoff is the first retry delay; it doubles with a little
	// jitter up to MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// Logger receives one line per significant event.
	Logger func(string)
	// Sleeper waits between attempts. Tests override it to avoid real waits.
	Sleeper func(context.Context, time.Duration) error
	// Rand seeds backoff jitter.
	Rand *rand.Rand
}

// Result summarizes one Run.
type Result struct {
	Version   string
	Chunks    int
	Attempts  map[int]int
	Completed bool // true when the final marker was newly written
}

// Fetcher downloads and resumes chunked objects from a Source.
type Fetcher struct {
	cfg Config
	// latestVersion is the version directory chunk parts are written into
	// during the current run.
	latestVersion string
}

// New builds a Fetcher with sensible defaults.
func New(cfg Config) *Fetcher {
	if cfg.Attempts <= 0 {
		cfg.Attempts = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 10 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = func(string) {}
	}
	if cfg.Sleeper == nil {
		cfg.Sleeper = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Fetcher{cfg: cfg}
}

// Run fetches the current object of src to completion (or until ctx is done).
func (f *Fetcher) Run(ctx context.Context, src Source) (*Result, error) {
	man, err := src.Manifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("chunkfetch: read manifest: %w", err)
	}
	if err := man.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(f.cfg.Dir, 0o755); err != nil {
		return nil, err
	}

	res := &Result{Version: man.Version, Chunks: man.ChunkCount(), Attempts: map[int]int{}}

	// A marker for another version is meaningless: discard the old run
	// entirely before looking at anything else.
	switch readMarkerState(f.cfg.Dir, man) {
	case markerValid:
		f.cfg.Logger("marker present and final digest verified; nothing to do")
		return res, nil
	case markerVersionMismatch:
		f.cfg.Logger("marker belongs to an older version; wiping old progress")
		if err := f.wipe(); err != nil {
			return nil, err
		}
	case markerCorrupt:
		f.cfg.Logger("marker present but unverifiable; wiping and re-fetching")
		if err := f.wipe(); err != nil {
			return nil, err
		}
	}

	bitmap, err := f.loadOrInitBitmap(man)
	if err != nil {
		return nil, err
	}

	// Any chunk part whose digest no longer matches its manifest entry must be
	// re-fetched, whether it was truncated, tampered with, or half-written.
	for i := 0; i < man.ChunkCount(); i++ {
		if !bitmap.Get(i) {
			continue
		}
		ok, err := f.chunkPartGood(i, man)
		if err != nil {
			return nil, err
		}
		if !ok {
			f.cfg.Logger(fmt.Sprintf("chunk %d stored but invalid; re-fetching", i))
			bitmap.Clear(i)
			if err := f.removePart(i); err != nil {
				return nil, err
			}
		}
	}

	for i := 0; i < man.ChunkCount(); i++ {
		if bitmap.Get(i) {
			continue
		}
		if err := f.fetchChunkWithRetry(ctx, src, man, i, bitmap, res); err != nil {
			// A version change mid-run invalidates all progress.
			var vmErr *VersionMismatchError
			if errors.As(err, &vmErr) {
				f.cfg.Logger("source version changed during fetch; wiping old progress")
				if werr := f.wipe(); werr != nil {
					return nil, werr
				}
			}
			return nil, err
		}
	}

	if err := f.assembleAndFinish(man); err != nil {
		return nil, err
	}
	res.Completed = true
	return res, nil
}

func (f *Fetcher) fetchChunkWithRetry(
	ctx context.Context, src Source, man *Manifest, i int, bm *bitmap, res *Result,
) error {
	var lastErr error
	backoff := f.cfg.InitialBackoff
	for attempt := 1; attempt <= f.cfg.Attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		res.Attempts[i] = attempt
		f.cfg.Logger(fmt.Sprintf("chunk %d: attempt %d/%d", i, attempt, f.cfg.Attempts))
		err := f.fetchChunkOnce(ctx, src, man, i)
		if err == nil {
			bm.Set(i)
			if perr := f.saveProgress(man, bm); perr != nil {
				return perr
			}
			f.cfg.Logger(fmt.Sprintf("chunk %d verified and committed", i))
			return nil
		}
		var vmErr *VersionMismatchError
		if errors.As(err, &vmErr) || errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		lastErr = err
		f.cfg.Logger(fmt.Sprintf("chunk %d: %v", i, err))
		if attempt < f.cfg.Attempts {
			wait := backoff + time.Duration(f.cfg.Rand.Int63n(int64(backoff)/2+1))
			if wait > f.cfg.MaxBackoff {
				wait = f.cfg.MaxBackoff
			}
			if serr := f.cfg.Sleeper(ctx, wait); serr != nil {
				return serr
			}
			if backoff *= 2; backoff > f.cfg.MaxBackoff {
				backoff = f.cfg.MaxBackoff
			}
		}
	}
	return fmt.Errorf("chunkfetch: chunk %d failed after %d attempts: %w",
		i, f.cfg.Attempts, lastErr)
}

func (f *Fetcher) fetchChunkOnce(ctx context.Context, src Source, man *Manifest, i int) error {
	off, length := man.ChunkRange(i)
	// The declared on-wire payload length must equal the chunk length; that
	// value is the allocation quota checked before any payload is read.
	resp, err := src.ReadAt(ctx, man.Version, off, length)
	if err != nil {
		return err
	}
	frame, err := ReadFrame(bytes.NewReader(resp), uint32(length))
	if err != nil {
		return fmt.Errorf("chunk %d frame: %w", i, err)
	}
	if int64(len(frame)) != length {
		return fmt.Errorf("%w: chunk %d length %d want %d", ErrChunkDigest, i, len(frame), length)
	}
	sum := sha256.Sum256(frame)
	if !bytes.Equal(sum[:], man.ChunkSHA256[i]) {
		return fmt.Errorf("%w: chunk %d", ErrChunkDigest, i)
	}
	return f.writePartAtomic(i, frame)
}

func (f *Fetcher) assembleAndFinish(man *Manifest) error {
	tmp, err := os.CreateTemp(f.cfg.Dir, ".object-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	// Stream every verified part once into the output file, the per-chunk
	// hasher, and the whole-object hasher simultaneously.
	whole := sha256.New()
	var assembled int64
	for i := 0; i < man.ChunkCount(); i++ {
		part, err := os.Open(f.partPath(i))
		if err != nil {
			cleanup()
			return fmt.Errorf("chunkfetch: missing verified chunk %d at assembly: %w", i, err)
		}
		chunkH := sha256.New()
		n, err := io.Copy(io.MultiWriter(tmp, chunkH, whole), part)
		part.Close()
		if err != nil {
			cleanup()
			return err
		}
		assembled += n
		if !bytes.Equal(chunkH.Sum(nil), man.ChunkSHA256[i]) {
			cleanup()
			return fmt.Errorf("%w: chunk %d re-check during assembly", ErrChunkDigest, i)
		}
	}

	if assembled != man.Size {
		cleanup()
		return fmt.Errorf("chunkfetch: assembled size %d want %d", assembled, man.Size)
	}
	if !bytes.Equal(whole.Sum(nil), man.WholeSHA256) {
		cleanup()
		return errors.New("chunkfetch: whole digest mismatch; refusing to publish or mark complete")
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	final := filepath.Join(f.cfg.Dir, fileOutput)
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return err
	}
	// The completion marker is written last, only after the final
	// whole-object digest check passed.
	return f.writeMarker(man)
}

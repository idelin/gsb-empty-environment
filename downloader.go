package paradl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
)

// ErrSimulatedCrash is returned when the fault file triggers a crash at a
// checkpoint save cut point.
var ErrSimulatedCrash = errors.New("simulated crash at checkpoint save")

// errRoundInvalidated cancels the current round after an entity version change.
var errRoundInvalidated = errors.New("entity version changed; round invalidated")

// Downloader pulls a source into a local file in parallel ranged chunks.
type Downloader struct {
	Src            Source
	DstPath        string
	CheckpointPath string
	ChunkSize      int64
	Workers        int
	Faults         *FaultFile

	mu           sync.Mutex
	confirmed    ConfirmedSet
	version      string
	size         int64
	ckptSaves    int
	roundInvalid bool
	file         *os.File
}

// NewDownloader builds a downloader with sane defaults.
func NewDownloader(src Source, dstPath, ckptPath string, faults *FaultFile) *Downloader {
	if faults == nil {
		faults = &FaultFile{}
	}
	return &Downloader{
		Src:            src,
		DstPath:        dstPath,
		CheckpointPath: ckptPath,
		ChunkSize:      1 << 16,
		Workers:        4,
		Faults:         faults,
	}
}

// Run downloads until the file is complete or an error occurs.
func (d *Downloader) Run(ctx context.Context) error {
	if d.ChunkSize <= 0 {
		return fmt.Errorf("chunk size must be positive")
	}
	if d.Workers <= 0 {
		d.Workers = 1
	}

	size, version, err := d.Src.Head(ctx)
	if err != nil {
		return fmt.Errorf("head: %w", err)
	}
	d.size, d.version = size, version

	d.file, err = os.OpenFile(d.DstPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open dst: %w", err)
	}
	defer d.file.Close()

	if err := d.resume(); err != nil {
		return err
	}

	for !d.confirmed.Covered(d.size) {
		if err := d.runRound(ctx); err != nil {
			if errors.Is(err, errRoundInvalidated) {
				if err := d.restartRound(ctx); err != nil {
					return err
				}
				continue
			}
			return err
		}
	}
	return d.file.Sync()
}

// resume loads the checkpoint and re-verifies every confirmed range against
// the on-disk file before trusting it.
func (d *Downloader) resume() error {
	ck, err := LoadCheckpoint(d.CheckpointPath)
	if errors.Is(err, ErrNoCheckpoint) {
		return d.truncate()
	}
	if err != nil {
		// Corrupt checkpoint: discard and start over.
		_ = os.Remove(d.CheckpointPath)
		return d.truncate()
	}
	if ck.Version != d.version || ck.Size != d.size {
		// Entity changed while we were away: everything is stale.
		_ = os.Remove(d.CheckpointPath)
		return d.truncate()
	}
	if err := d.file.Truncate(d.size); err != nil {
		return fmt.Errorf("size dst: %w", err)
	}
	for _, e := range ck.Confirmed {
		sum, err := ChecksumReaderAt(d.file, e.Start, e.End)
		if err != nil {
			return fmt.Errorf("verify confirmed %s: %w", e.ByteRange, err)
		}
		if sum != e.Checksum {
			// On-disk bytes no longer match what was confirmed; drop the
			// entry so the range is fetched again.
			continue
		}
		d.confirmed.Add(e)
	}
	return nil
}

func (d *Downloader) truncate() error {
	d.confirmed = ConfirmedSet{}
	if err := d.file.Truncate(d.size); err != nil {
		return fmt.Errorf("truncate dst: %w", err)
	}
	return nil
}

// restartRound handles an entity version change: every confirmation from the
// old version is void, so the round (and the checkpoint) starts empty.
func (d *Downloader) restartRound(ctx context.Context) error {
	size, version, err := d.Src.Head(ctx)
	if err != nil {
		return fmt.Errorf("head after version change: %w", err)
	}
	d.size, d.version = size, version
	d.roundInvalid = false
	if err := d.truncate(); err != nil {
		return err
	}
	_ = os.Remove(d.CheckpointPath)
	return nil
}

// runRound fetches all currently-missing ranges with a worker pool.
func (d *Downloader) runRound(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	queue := make(chan ByteRange)
	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	report := func(err error) {
		select {
		case errCh <- err:
		default:
		}
		cancel()
	}

	for i := 0; i < d.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range queue {
				err := d.fetchOne(ctx, r)
				if errors.Is(err, errRoundInvalidated) {
					// Not a real failure: the flag is set and the round
					// restarts, so don't let it mask other errors.
					cancel()
					return
				}
				if err != nil {
					report(err)
					return
				}
			}
		}()
	}

	missing := d.confirmed.Missing(d.size)
feed:
	for _, gap := range missing {
		for start := gap.Start; start < gap.End; start += d.ChunkSize {
			end := min(start+d.ChunkSize, gap.End)
			select {
			case queue <- ByteRange{Start: start, End: end}:
			case <-ctx.Done():
				break feed
			}
		}
	}
	close(queue)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
	}
	if d.roundInvalid {
		return errRoundInvalidated
	}
	return nil
}

// fetchOne fetches one requested range and confirms what actually arrived.
func (d *Downloader) fetchOne(ctx context.Context, want ByteRange) error {
	resp, err := d.Src.Fetch(ctx, want)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", want, err)
	}
	return d.handleResponse(want, resp)
}

// handleResponse validates, writes, verifies and confirms a response.
func (d *Downloader) handleResponse(want ByteRange, resp Response) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// A response from another entity version is stale; it must not touch
	// any state. If the source has moved on, the whole round is void.
	if resp.Version != d.version {
		d.roundInvalid = true
		return errRoundInvalidated
	}

	// Only the actual range counts, clipped to the entity. It may extend
	// beyond the request; overlapping confirmations are reconciled below.
	actual := resp.Range.Overlap(ByteRange{Start: 0, End: d.size})
	if actual.Empty() {
		return nil
	}
	off := actual.Start - resp.Range.Start
	data := resp.Data[off : off+actual.Len()]

	// The response must be internally consistent before we trust a byte.
	if int64(len(resp.Data)) != resp.Range.Len() {
		return fmt.Errorf("response length %d does not match range %s", len(resp.Data), resp.Range)
	}
	if resp.Checksum != "" && ChecksumBytes(resp.Data) != resp.Checksum {
		return fmt.Errorf("response checksum mismatch for %s", resp.Range)
	}

	// Overlapping confirmations must agree byte-for-byte to merge.
	for _, e := range d.confirmed.Overlaps(actual) {
		ov := e.Overlap(actual)
		buf := make([]byte, ov.Len())
		if _, err := d.file.ReadAt(buf, ov.Start); err != nil {
			return fmt.Errorf("read overlap %s: %w", ov, err)
		}
		newBytes := data[ov.Start-actual.Start : ov.End-actual.Start]
		if ChecksumBytes(buf) != ChecksumBytes(newBytes) {
			return fmt.Errorf("conflicting overlapping responses at %s", ov)
		}
	}

	// Write the full actual range, then read it back and verify: only a
	// range that is completely on disk and checksum-clean gets confirmed.
	if _, err := d.file.WriteAt(data, actual.Start); err != nil {
		return fmt.Errorf("write %s: %w", actual, err)
	}
	sum, err := ChecksumReaderAt(d.file, actual.Start, actual.End)
	if err != nil {
		return fmt.Errorf("verify written %s: %w", actual, err)
	}
	if sum != ChecksumBytes(data) {
		return fmt.Errorf("written range %s failed verification", actual)
	}

	d.confirmed.Add(RangeEntry{ByteRange: actual, Checksum: sum})
	return d.saveCheckpointLocked()
}

// saveCheckpointLocked persists progress atomically, honoring the crash
// cut point from the fault file.
func (d *Downloader) saveCheckpointLocked() error {
	d.ckptSaves++
	if n := d.Faults.CrashOnCheckpointSave; n > 0 && d.ckptSaves == n {
		return ErrSimulatedCrash
	}
	ck := &Checkpoint{
		Version:   d.version,
		Size:      d.size,
		ChunkSize: d.ChunkSize,
		Confirmed: d.confirmed.Entries(),
	}
	return SaveCheckpoint(d.CheckpointPath, ck)
}

// Package importer copies newline-delimited records from a source file to a
// target store in numbered batches, persisting a checkpoint (record-boundary
// offset, source-prefix digest, target batch number) after every committed
// batch so that an interrupted run can resume without loss or duplication.
package importer

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
)

// Resume-time verification failures. All of them stop the importer before
// any record is (re)committed.
var (
	// ErrSourcePrefixMismatch means the bytes before the checkpointed
	// offset no longer match the checkpointed digest: the source was
	// modified (or truncated) inside the already-imported region.
	ErrSourcePrefixMismatch = errors.New("importer: source prefix mismatch")
	// ErrTargetBatchMismatch means the target's last committed batch
	// number is inconsistent with the checkpoint (target regressed, or
	// advanced beyond what one lost receipt can explain).
	ErrTargetBatchMismatch = errors.New("importer: target batch mismatch")
)

// Store is the import target. Implementations must make CommitBatch
// idempotent per batch number: recommitting the same records under the same
// batch number must succeed without duplicating them, and recommitting
// different records under an already-used number must fail.
type Store interface {
	// LastBatch returns the highest committed batch number (0 if none).
	LastBatch() (uint64, error)
	// CommitBatch atomically commits records as the given batch number.
	CommitBatch(batch uint64, records [][]byte) error
}

// Importer copies records from SourcePath to Store.
type Importer struct {
	SourcePath     string
	CheckpointPath string
	Store          Store
	// BatchSize is the maximum number of records per committed batch.
	BatchSize int
}

// Run imports until EOF (or the last complete record boundary) and returns
// nil. It is safe to call again after a crash or a previous error: it resumes
// from the checkpoint after verifying the source prefix and the target batch
// number.
func (im *Importer) Run() error {
	batchSize := im.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	cp, found, err := loadCheckpoint(im.CheckpointPath)
	if err != nil {
		return err
	}
	if !found {
		cp = Checkpoint{Offset: 0, PrefixDigest: digestOfEmpty(), Batch: 0}
	}

	f, err := os.Open(im.SourcePath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Verify the source prefix and position the file at the checkpointed
	// record boundary. The running hash is reused for the next checkpoint.
	h, err := verifyPrefix(f, cp)
	if err != nil {
		return err
	}

	// Verify the target batch number. The target may be exactly at the
	// checkpointed batch, or one batch ahead if the previous run committed
	// a batch but crashed before saving the checkpoint (lost receipt); the
	// idempotent recommit below reconciles that case. Anything else is an
	// inconsistent state and must stop.
	last, err := im.Store.LastBatch()
	if err != nil {
		return err
	}
	switch {
	case last < cp.Batch:
		return fmt.Errorf("%w: target at batch %d, checkpoint at batch %d", ErrTargetBatchMismatch, last, cp.Batch)
	case last > cp.Batch+1:
		return fmt.Errorf("%w: target at batch %d, checkpoint at batch %d", ErrTargetBatchMismatch, last, cp.Batch)
	}

	reader := bufio.NewReader(f)
	offset := cp.Offset
	batch := cp.Batch
	for {
		raw, records, next, err := readBatch(reader, offset, batchSize)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		batch++
		for _, line := range raw {
			h.Write(line)
		}
		if err := im.Store.CommitBatch(batch, records); err != nil {
			return fmt.Errorf("importer: commit batch %d: %w", batch, err)
		}
		cp = Checkpoint{Offset: next, PrefixDigest: hex.EncodeToString(h.Sum(nil)), Batch: batch}
		if err := saveCheckpoint(im.CheckpointPath, cp); err != nil {
			return err
		}
		offset = next
	}
}

// verifyPrefix hashes source[:cp.Offset], compares it with the checkpointed
// digest, and leaves f positioned exactly at cp.Offset. The returned hash
// already contains the verified prefix.
func verifyPrefix(f *os.File, cp Checkpoint) (hash.Hash, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < cp.Offset {
		return nil, fmt.Errorf("%w: source truncated to %d bytes, checkpoint offset is %d",
			ErrSourcePrefixMismatch, info.Size(), cp.Offset)
	}
	h := sha256.New()
	if _, err := io.CopyN(h, f, cp.Offset); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSourcePrefixMismatch, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != cp.PrefixDigest {
		return nil, fmt.Errorf("%w: digest of first %d bytes is %s, checkpoint has %s",
			ErrSourcePrefixMismatch, cp.Offset, got, cp.PrefixDigest)
	}
	return h, nil
}

// readBatch reads up to batchSize complete records starting at offset. It
// returns the raw record bytes (newline included, for hashing), the record
// payloads (newline stripped, for the store), and the offset of the next
// record boundary. A trailing partial line at EOF is left unconsumed: it may
// still be completed by a future append, so the offset stays on the last
// complete record boundary.
func readBatch(reader *bufio.Reader, offset int64, batchSize int) (raw [][]byte, records [][]byte, next int64, err error) {
	next = offset
	for len(records) < batchSize {
		line, rerr := reader.ReadBytes('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil, nil, 0, rerr
		}
		if rerr != nil {
			// EOF: only complete lines (newline-terminated) count as records.
			break
		}
		raw = append(raw, line)
		records = append(records, trimLineEnding(line))
		next += int64(len(line))
	}
	return raw, records, next, nil
}

func trimLineEnding(line []byte) []byte {
	line = line[:len(line)-1] // drop '\n'
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return line
}

func digestOfEmpty() string {
	sum := sha256.Sum256(nil)
	return hex.EncodeToString(sum[:])
}

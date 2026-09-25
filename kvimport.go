// Package kvimport streams key-value records from an input into a
// caller-specified directory, with crash-safe checkpoints, quarantine of
// malformed records, and atomic final publish.
//
// Input format: one JSON object per line, {"k": "key", "v": "value"}.
//
// Files created inside Options.Dir:
//
//	data.log        staging log of confirmed records (not user-visible output)
//	rejects.log     quarantined malformed records (JSON lines)
//	checkpoint.json input digest + last confirmed offset + staging sizes
//	store.json      final published snapshot (appears only via atomic rename)
//
// Incomplete batches live only in the staging files; store.json is created
// by a single atomic rename after the whole input has been consumed, so
// readers never observe a partially imported batch.
package kvimport

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	dataFileName       = "data.log"
	rejectsFileName    = "rejects.log"
	checkpointFileName = "checkpoint.json"
	storeFileName      = "store.json"
)

// Fault injection points passed to Options.Inject. Returning an error from
// Inject simulates a crash at that exact point: everything fsynced before
// the point survives, everything after is lost.
const (
	// PointAfterDataSync fires after a batch was appended to the staging
	// files and fsynced, but before the checkpoint covering it was written.
	PointAfterDataSync = "afterDataSync"
	// PointAfterCheckpoint fires after the checkpoint for a batch was
	// durably written.
	PointAfterCheckpoint = "afterCheckpoint"
	// PointBeforePublish fires after the input was fully consumed, before
	// the final snapshot is staged.
	PointBeforePublish = "beforePublish"
	// PointAfterPublishRename fires after store.json was atomically renamed
	// into place, before the directory is fsynced.
	PointAfterPublishRename = "afterPublishRename"
)

// Options configures a single Import run.
type Options struct {
	// Dir is the caller-specified directory holding all state and output
	// files. It is created if missing.
	Dir string
	// BatchSize is the number of good records per confirmed batch.
	// Defaults to 1000.
	BatchSize int
	// Inject, if non-nil, is called at every fault injection point.
	// Returning an error aborts the import as if the process crashed.
	Inject func(point string, batch int) error
}

// Result summarizes a completed import. Counts are cumulative, including
// work done by previous crashed runs that was recovered.
type Result struct {
	Records   uint64 // good records applied
	Rejects   uint64 // malformed records quarantined
	Keys      int    // distinct keys in the published snapshot
	Offset    int64  // confirmed input bytes (last confirmed offset)
	Digest    string // sha256 hex of input[0:Offset]
	StorePath string // path of the published snapshot
	Resumed   bool   // true if this run recovered from a checkpoint
}

type kvEntry struct {
	Value   string
	Version uint64
}

type importer struct {
	opts        Options
	data        *os.File
	rej         *os.File
	state       map[string]*kvEntry
	h           hash.Hash
	pos         int64 // input bytes consumed so far (all hashed)
	dataSize    int64 // confirmed size of data.log
	rejectsSize int64 // confirmed size of rejects.log
	records     uint64
	rejects     uint64
	batch       int
}

// Import streams records from input into opts.Dir and publishes a snapshot.
// If a checkpoint from a previous crashed run exists, Import verifies that
// the confirmed input prefix is unchanged (otherwise it refuses to resume),
// discards the unconfirmed staging tail, and continues from the last
// confirmed offset. Replaying the last unconfirmed batch is idempotent and
// does not grow key versions.
func Import(input io.Reader, opts Options) (*Result, error) {
	if opts.Dir == "" {
		return nil, errors.New("kvimport: Options.Dir is required")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}

	im := &importer{
		opts:  opts,
		state: make(map[string]*kvEntry),
		h:     sha256.New(),
	}
	defer im.closeFiles()

	resumed, err := im.recover(input)
	if err != nil {
		return nil, err
	}
	if err := im.run(bufio.NewReaderSize(input, 64<<10)); err != nil {
		return nil, err
	}
	storePath, err := im.publish()
	if err != nil {
		return nil, err
	}
	return &Result{
		Records:   im.records,
		Rejects:   im.rejects,
		Keys:      len(im.state),
		Offset:    im.pos,
		Digest:    hex.EncodeToString(im.h.Sum(nil)),
		StorePath: storePath,
		Resumed:   resumed,
	}, nil
}

// recover loads a previous checkpoint if present. It consumes exactly
// checkpoint.Offset bytes from input to verify the prefix digest, leaving
// the reader positioned at the last confirmed offset.
func (im *importer) recover(input io.Reader) (bool, error) {
	cpBytes, err := os.ReadFile(im.path(checkpointFileName))
	if errors.Is(err, fs.ErrNotExist) {
		// Fresh import: any staging files are unconfirmed leftovers from a
		// crash before the first checkpoint; discard them.
		if err := im.openFiles(true); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	cp, err := parseCheckpoint(cpBytes)
	if err != nil {
		return false, err
	}
	if err := im.verifyInputPrefix(input, cp.Offset, cp.Digest); err != nil {
		return false, err
	}
	if err := im.openFiles(false); err != nil {
		return false, err
	}
	if err := im.truncateTo(cp); err != nil {
		return false, err
	}
	if err := im.loadData(cp); err != nil {
		return false, err
	}
	rejects, err := im.countRejects(cp.RejectsSize)
	if err != nil {
		return false, err
	}
	im.rejects = rejects
	im.pos = cp.Offset
	im.dataSize = cp.DataSize
	im.rejectsSize = cp.RejectsSize
	return true, nil
}

// verifyInputPrefix hashes exactly offset bytes from input and compares
// against the checkpoint digest. Any change to the confirmed prefix
// (content, length, or order) is rejected.
func (im *importer) verifyInputPrefix(input io.Reader, offset int64, wantDigest string) error {
	if _, err := io.CopyN(im.h, input, offset); err != nil {
		return fmt.Errorf("kvimport: input changed: cannot re-read %d confirmed bytes: %w", offset, err)
	}
	if got := hex.EncodeToString(im.h.Sum(nil)); got != wantDigest {
		return fmt.Errorf("kvimport: input changed: digest mismatch over first %d bytes (checkpoint %s, got %s)", offset, wantDigest, got)
	}
	return nil
}

func (im *importer) openFiles(truncate bool) error {
	flags := os.O_CREATE | os.O_RDWR | os.O_APPEND
	if truncate {
		flags |= os.O_TRUNC
	}
	var err error
	if im.data, err = os.OpenFile(im.path(dataFileName), flags, 0o644); err != nil {
		return err
	}
	if im.rej, err = os.OpenFile(im.path(rejectsFileName), flags, 0o644); err != nil {
		return err
	}
	return nil
}

// truncateTo drops the unconfirmed tail of the staging files: anything
// written after the last checkpoint (including torn partial lines) is cut
// back to the sizes recorded in the checkpoint.
func (im *importer) truncateTo(cp *checkpoint) error {
	for _, f := range []struct {
		file *os.File
		want int64
		name string
	}{
		{im.data, cp.DataSize, dataFileName},
		{im.rej, cp.RejectsSize, rejectsFileName},
	} {
		st, err := f.file.Stat()
		if err != nil {
			return err
		}
		if st.Size() < f.want {
			return fmt.Errorf("kvimport: %s is %d bytes but checkpoint expects %d: staging corrupt", f.name, st.Size(), f.want)
		}
		if err := f.file.Truncate(f.want); err != nil {
			return err
		}
	}
	return nil
}

// loadData rebuilds the in-memory state from the confirmed prefix of
// data.log. This is what makes replay idempotent: versions are derived
// from the confirmed log, never from unconfirmed leftovers.
func (im *importer) loadData(cp *checkpoint) error {
	if _, err := im.data.Seek(0, io.SeekStart); err != nil {
		return err
	}
	sc := bufio.NewScanner(io.LimitReader(im.data, cp.DataSize))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	line := 0
	for sc.Scan() {
		line++
		var e dataEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return fmt.Errorf("kvimport: %s corrupt at line %d: %w", dataFileName, line, err)
		}
		if e.Off > cp.Offset {
			return fmt.Errorf("kvimport: %s line %d covers unconfirmed offset %d", dataFileName, line, e.Off)
		}
		im.apply(e.K, e.V)
		im.records++
	}
	if err := sc.Err(); err != nil {
		return err
	}
	_, err := im.data.Seek(0, io.SeekEnd)
	return err
}

func (im *importer) countRejects(size int64) (uint64, error) {
	if _, err := im.rej.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	buf, err := io.ReadAll(io.LimitReader(im.rej, size))
	if err != nil {
		return 0, err
	}
	n := uint64(bytes.Count(buf, []byte{'\n'}))
	_, err = im.rej.Seek(0, io.SeekEnd)
	return n, err
}

// run consumes the remaining input, committing a batch (staging append +
// fsync + checkpoint) every BatchSize good records and once at EOF.
func (im *importer) run(br *bufio.Reader) error {
	var pendData, pendRej bytes.Buffer
	pending := 0
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			if rerr == io.EOF {
				// Truncated tail: bytes without a terminating newline are
				// never committed as a record. They are consumed (so the
				// offset/digest cover them) and quarantined as truncated.
				im.consume(line)
				writeReject(&pendRej, im.pos, line, "truncated record: missing trailing newline")
				im.rejects++
				return im.commitBatch(&pendData, &pendRej)
			}
			im.consume(line)
			k, v, reason, skip := parseRecord(line)
			switch {
			case skip:
				// Blank line: consumed, neither record nor reject.
			case reason != "":
				writeReject(&pendRej, im.pos, line, reason)
				im.rejects++
			default:
				writeData(&pendData, im.pos, k, v)
				im.apply(k, v)
				im.records++
				pending++
			}
		}
		if rerr == io.EOF {
			return im.commitBatch(&pendData, &pendRej)
		}
		if rerr != nil {
			return rerr
		}
		if pending >= im.opts.BatchSize {
			if err := im.commitBatch(&pendData, &pendRej); err != nil {
				return err
			}
			pending = 0
		}
	}
}

// commitBatch durably confirms one batch: append staging entries, fsync,
// then atomically replace the checkpoint. The checkpoint is the commit
// point; a crash before it leaves the batch unconfirmed and replayable.
func (im *importer) commitBatch(pendData, pendRej *bytes.Buffer) error {
	im.batch++
	if pendRej.Len() > 0 {
		if _, err := im.rej.Write(pendRej.Bytes()); err != nil {
			return err
		}
		im.rejectsSize += int64(pendRej.Len())
	}
	if pendData.Len() > 0 {
		if _, err := im.data.Write(pendData.Bytes()); err != nil {
			return err
		}
		im.dataSize += int64(pendData.Len())
	}
	if err := im.rej.Sync(); err != nil {
		return err
	}
	if err := im.data.Sync(); err != nil {
		return err
	}
	if err := im.inject(PointAfterDataSync); err != nil {
		return err
	}
	if err := im.writeCheckpoint(); err != nil {
		return err
	}
	if err := im.inject(PointAfterCheckpoint); err != nil {
		return err
	}
	pendData.Reset()
	pendRej.Reset()
	return nil
}

func (im *importer) writeCheckpoint() error {
	cp := checkpoint{
		Schema:      1,
		Offset:      im.pos,
		Digest:      hex.EncodeToString(im.h.Sum(nil)),
		DataSize:    im.dataSize,
		RejectsSize: im.rejectsSize,
	}
	cp.Checksum = cp.checksum()
	b, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return writeFileAtomic(im.opts.Dir, checkpointFileName, b)
}

// publish writes the final snapshot to a temp file, fsyncs it, and
// atomically renames it over store.json. Before the rename, no incomplete
// batch is visible to readers of store.json.
func (im *importer) publish() (string, error) {
	if err := im.inject(PointBeforePublish); err != nil {
		return "", err
	}
	snap := snapshot{
		Meta: snapshotMeta{
			Records: im.records,
			Rejects: im.rejects,
			Keys:    len(im.state),
			Offset:  im.pos,
			Digest:  hex.EncodeToString(im.h.Sum(nil)),
		},
		Keys: make(map[string]snapshotValue, len(im.state)),
	}
	for k, e := range im.state {
		snap.Keys[k] = snapshotValue{Value: e.Value, Version: e.Version}
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := im.path(storeFileName + ".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	storePath := im.path(storeFileName)
	if err := os.Rename(tmp, storePath); err != nil {
		return "", err
	}
	if err := im.inject(PointAfterPublishRename); err != nil {
		return "", err
	}
	if err := syncDir(im.opts.Dir); err != nil {
		return "", err
	}
	return storePath, nil
}

func (im *importer) apply(k, v string) {
	e, ok := im.state[k]
	if !ok {
		e = &kvEntry{}
		im.state[k] = e
	}
	e.Value = v
	e.Version++
}

func (im *importer) consume(p []byte) {
	im.h.Write(p)
	im.pos += int64(len(p))
}

func (im *importer) inject(point string) error {
	if im.opts.Inject == nil {
		return nil
	}
	if err := im.opts.Inject(point, im.batch); err != nil {
		return fmt.Errorf("kvimport: injected fault at %s (batch %d): %w", point, im.batch, err)
	}
	return nil
}

func (im *importer) path(name string) string { return filepath.Join(im.opts.Dir, name) }

func (im *importer) closeFiles() {
	if im.data != nil {
		im.data.Close()
	}
	if im.rej != nil {
		im.rej.Close()
	}
}

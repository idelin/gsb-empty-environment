package kvimport

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	workDirName    = "work"
	checkpointName = "checkpoint.json"
	walName        = "data.wal"
	rejectsName    = "rejects.log"
	publishedName  = "store.db"

	headHashBytes    = 64 << 10
	defaultBatchSize = 1024
)

// Config configures one import run. All working and output files live
// under Dir; nothing is written outside it.
type Config struct {
	// Dir is the caller-specified directory holding the work files
	// (work/) and the published store (store.db).
	Dir string
	// InputPath is the record file to import ("key=value\n" lines).
	InputPath string
	// BatchSize is the number of complete records confirmed per
	// checkpoint. Defaults to 1024.
	BatchSize int
	// Injector, if non-nil, receives fault-injection callbacks.
	Injector FaultInjector
}

// Result summarizes a run.
type Result struct {
	Resumed      bool // recovered from a previous checkpoint
	AlreadyDone  bool // a previous run had already published; nothing to do
	Batches      int  // checkpoints written by this run
	KeysTotal    int  // keys in the published store
	RejectsTotal int  // total quarantined records (including earlier runs)
}

// rejectEntry is one quarantined record in rejects.log.
type rejectEntry struct {
	Offset int64  `json:"offset"`
	Reason string `json:"reason"`
	Raw    string `json:"raw"`
}

// Run executes the import, resuming from the last confirmed checkpoint if
// one exists. It is safe to call again after a crash at any point.
func Run(cfg Config) (Result, error) {
	var res Result
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	inj := cfg.Injector
	if inj == nil {
		inj = NoopInjector{}
	}

	absInput, err := filepath.Abs(cfg.InputPath)
	if err != nil {
		return res, err
	}
	fi, err := os.Stat(absInput)
	if err != nil {
		return res, fmt.Errorf("stat input: %w", err)
	}
	if fi.IsDir() {
		return res, fmt.Errorf("input %s is a directory", absInput)
	}
	head, err := hashHead(absInput)
	if err != nil {
		return res, err
	}

	workDir := filepath.Join(cfg.Dir, workDirName)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return res, err
	}
	walPath := filepath.Join(workDir, walName)
	rejectsPath := filepath.Join(workDir, rejectsName)

	ck, err := loadCheckpoint(workDir)
	if err != nil {
		return res, err
	}

	var hasher hash.Hash
	var offset int64

	if ck != nil {
		if err := verifyInputIdentity(ck, absInput, fi, head); err != nil {
			return res, err
		}
		if ck.Done {
			if st, err := LoadPublished(cfg.Dir); err == nil {
				res.AlreadyDone = true
				res.KeysTotal = st.Len()
				return res, nil
			}
			// Published store missing although the checkpoint says Done:
			// fall through and republish from the confirmed WAL.
		}
		// Prove the consumed prefix is unchanged before trusting Offset.
		h, err := hashPrefix(absInput, ck.Offset)
		if err != nil {
			return res, err
		}
		if hex.EncodeToString(h.Sum(nil)) != ck.PrefixSHA256 {
			return res, &InputChangedError{
				Reason: fmt.Sprintf("prefix digest mismatch at confirmed offset %d", ck.Offset),
			}
		}
		hasher = h
		offset = ck.Offset
		res.Resumed = true

		// Discard anything appended after the last confirmed checkpoint
		// (e.g. a crash between WAL fsync and checkpoint write).
		if err := os.Truncate(walPath, ck.WalLen); err != nil {
			return res, fmt.Errorf("truncate wal: %w", err)
		}
		if err := os.Truncate(rejectsPath, ck.RejectsLen); err != nil {
			return res, fmt.Errorf("truncate rejects: %w", err)
		}
	} else {
		// No checkpoint: nothing was ever confirmed. Start clean; any
		// stale work files from a run that crashed before its first
		// checkpoint are discarded.
		if err := os.WriteFile(walPath, nil, 0o644); err != nil {
			return res, err
		}
		if err := os.WriteFile(rejectsPath, nil, 0o644); err != nil {
			return res, err
		}
		hasher = sha256.New()
	}

	store, err := loadWal(walPath)
	if err != nil {
		return res, err
	}
	rejectsTotal, err := countLines(rejectsPath)
	if err != nil {
		return res, err
	}

	wal, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return res, err
	}
	defer wal.Close()
	rej, err := os.OpenFile(rejectsPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return res, err
	}
	defer rej.Close()

	in, err := os.Open(absInput)
	if err != nil {
		return res, err
	}
	defer in.Close()
	if _, err := in.Seek(offset, io.SeekStart); err != nil {
		return res, err
	}
	reader := bufio.NewReaderSize(in, 64<<10)

	// flush confirms one batch: hash consumed bytes, append WAL/reject
	// entries, fsync both, then atomically write the checkpoint.
	batch := make([][]byte, 0, cfg.BatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		for _, raw := range batch {
			hasher.Write(raw)
			recOffset := offset
			offset += int64(len(raw))

			key, value, reason := parseRecord(raw)
			switch reason {
			case "":
				entry, _ := json.Marshal(walEntry{Offset: recOffset, Key: key, Value: value})
				if _, err := wal.Write(append(entry, '\n')); err != nil {
					return err
				}
				store.Apply(recOffset, key, value)
			case "skip":
				// blank line: consumed but not a record
			default:
				entry, _ := json.Marshal(rejectEntry{Offset: recOffset, Reason: reason, Raw: string(raw)})
				if _, err := rej.Write(append(entry, '\n')); err != nil {
					return err
				}
				rejectsTotal++
			}
		}
		if err := wal.Sync(); err != nil {
			return err
		}
		if err := rej.Sync(); err != nil {
			return err
		}
		inj.OnFault(FaultBeforeCheckpoint)

		walLen, rejectsLen, err := fileLens(wal, rej)
		if err != nil {
			return err
		}
		if err := saveCheckpoint(workDir, &checkpoint{
			InputPath:    absInput,
			Size:         fi.Size(),
			MtimeNano:    fi.ModTime().UnixNano(),
			HeadSHA256:   head,
			Offset:       offset,
			PrefixSHA256: hex.EncodeToString(hasher.Sum(nil)),
			WalLen:       walLen,
			RejectsLen:   rejectsLen,
		}); err != nil {
			return err
		}
		res.Batches++
		inj.OnFault(FaultAfterCheckpoint)
		batch = batch[:0]
		return nil
	}

	// Stream the input. Only newline-terminated records are consumed; a
	// trailing partial line is never committed as half a record.
	var tail []byte
	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) > 0 {
			if err == nil {
				batch = append(batch, raw)
			} else {
				tail = append(tail, raw...)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return res, fmt.Errorf("read input: %w", err)
		}
		if len(batch) >= cfg.BatchSize {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}
	if err := flush(); err != nil {
		return res, err
	}

	// Quarantine an unterminated tail instead of committing half a record.
	if len(tail) > 0 {
		entry, _ := json.Marshal(rejectEntry{Offset: offset, Reason: "truncated tail (missing newline)", Raw: string(tail)})
		if _, err := rej.Write(append(entry, '\n')); err != nil {
			return res, err
		}
		rejectsTotal++
		if err := rej.Sync(); err != nil {
			return res, err
		}
	}

	// Publish: the finished snapshot becomes visible in a single atomic
	// rename. Incomplete batches never appear in store.db.
	inj.OnFault(FaultBeforePublish)
	if err := writeFileAtomic(cfg.Dir, publishedName, store.snapshot()); err != nil {
		return res, err
	}
	if err := syncDir(cfg.Dir); err != nil {
		return res, err
	}
	inj.OnFault(FaultAfterPublish)

	walLen, rejectsLen, err := fileLens(wal, rej)
	if err != nil {
		return res, err
	}
	if err := saveCheckpoint(workDir, &checkpoint{
		InputPath:    absInput,
		Size:         fi.Size(),
		MtimeNano:    fi.ModTime().UnixNano(),
		HeadSHA256:   head,
		Offset:       offset,
		PrefixSHA256: hex.EncodeToString(hasher.Sum(nil)),
		WalLen:       walLen,
		RejectsLen:   rejectsLen,
		Done:         true,
	}); err != nil {
		return res, err
	}

	res.KeysTotal = store.Len()
	res.RejectsTotal = rejectsTotal
	return res, nil
}

// parseRecord parses one raw input line ("key=value\n"). It returns
// reason == "" for a valid record, "skip" for a blank line, or a
// human-readable rejection reason otherwise.
func parseRecord(raw []byte) (key, value, reason string) {
	line := strings.TrimSuffix(string(raw), "\n")
	line = strings.TrimSuffix(line, "\r")
	if strings.TrimSpace(line) == "" {
		return "", "", "skip"
	}
	i := strings.IndexByte(line, '=')
	if i < 0 {
		return "", "", "missing '=' separator"
	}
	if i == 0 {
		return "", "", "empty key"
	}
	return line[:i], line[i+1:], ""
}

func verifyInputIdentity(ck *checkpoint, absPath string, fi os.FileInfo, head string) error {
	switch {
	case ck.InputPath != absPath:
		return &InputChangedError{Reason: fmt.Sprintf("path changed: %q -> %q", ck.InputPath, absPath)}
	case ck.Size != fi.Size():
		return &InputChangedError{Reason: fmt.Sprintf("size changed: %d -> %d", ck.Size, fi.Size())}
	case ck.MtimeNano != fi.ModTime().UnixNano():
		return &InputChangedError{Reason: "modification time changed"}
	case ck.HeadSHA256 != head:
		return &InputChangedError{Reason: "head digest changed"}
	}
	return nil
}

func hashHead(path string) (string, error) {
	h := sha256.New()
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.CopyN(h, f, headHashBytes); err != nil && err != io.EOF {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashPrefix returns a hasher pre-fed with input[0:n], used both to verify
// the checkpoint's prefix digest and to continue the running digest.
func hashPrefix(path string, n int64) (hash.Hash, error) {
	h := sha256.New()
	if n == 0 {
		return h, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := io.CopyN(h, f, n); err != nil {
		return nil, &InputChangedError{Reason: fmt.Sprintf("input shrank below confirmed offset %d", n)}
	}
	return h, nil
}

func fileLens(files ...*os.File) (int64, int64, error) {
	var lens []int64
	for _, f := range files {
		fi, err := f.Stat()
		if err != nil {
			return 0, 0, err
		}
		lens = append(lens, fi.Size())
	}
	return lens[0], lens[1], nil
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		n++
	}
	return n, sc.Err()
}

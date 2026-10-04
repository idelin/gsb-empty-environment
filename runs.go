package extsort

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Options controls run generation and the checkpointed merge.
type Options struct {
	Dir             string // working directory: runs, checkpoints, intermediates
	MemRecords      int    // max records held in memory per sorted run
	FanIn           int    // max input segments merged in one task
	CheckpointEvery int    // output records between checkpoints

	// CrashHook, when set, is invoked after every published checkpoint.
	// Returning an error aborts the merge immediately, simulating a
	// crash in which everything not covered by the checkpoint (unsynced
	// buffers, in-memory state) is lost. Call Merge again to resume.
	CrashHook func(info CrashInfo) error
}

// CrashInfo describes the checkpoint just published.
type CrashInfo struct {
	Task         string // id of the merge task that published
	OutputFile   string // absolute path of the task output
	Published    int    // checkpoints published so far in this Merge call
	PublishedLen int64  // trusted (synced) output prefix length
}

func (o Options) withDefaults() Options {
	if o.MemRecords <= 0 {
		o.MemRecords = 100000
	}
	if o.FanIn <= 0 {
		o.FanIn = 64
	}
	if o.FanIn < 2 {
		o.FanIn = 2 // fan-in 1 cannot reduce the run count
	}
	if o.CheckpointEvery <= 0 {
		o.CheckpointEvery = 10000
	}
	return o
}

const runsManifestName = "runs.json"

type runsManifest struct {
	Runs []InputBinding `json:"runs"`
}

// splitLine parses one input line "key\tvalue"; without a tab the whole
// line is the key and the value is empty.
func splitLine(line []byte) (key, value []byte) {
	if i := bytes.IndexByte(line, '\t'); i >= 0 {
		return line[:i], line[i+1:]
	}
	return line, nil
}

// GenerateReads reads the input segments in order, assigns each record a
// globally increasing uint64 sequence number (full precision, so stability
// never degrades for large inputs), and writes sorted run files into
// opts.Dir together with a runs.json manifest binding every run digest.
func GenerateRuns(inputs []string, opts Options) ([]InputBinding, error) {
	o := opts.withDefaults()
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, err
	}
	var runs []InputBinding
	var seq uint64
	buf := make([]Record, 0, o.MemRecords)

	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		sort.Slice(buf, func(i, j int) bool { return buf[i].Less(&buf[j]) })
		name := fmt.Sprintf("run-%06d.bin", len(runs))
		b, err := writeRun(filepath.Join(o.Dir, name), buf)
		if err != nil {
			return err
		}
		runs = append(runs, b)
		buf = buf[:0]
		return nil
	}

	for _, in := range inputs {
		f, err := countedOpen(in)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			key, value := splitLine(sc.Bytes())
			buf = append(buf, Record{
				Key:   string(key),
				Seq:   seq,
				Value: append([]byte(nil), value...),
			})
			seq++
			if len(buf) >= o.MemRecords {
				if err := flush(); err != nil {
					f.Close()
					return nil, err
				}
			}
		}
		if err := sc.Err(); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := saveJSONAtomic(filepath.Join(o.Dir, runsManifestName), &runsManifest{Runs: runs}); err != nil {
		return nil, err
	}
	return runs, nil
}

// writeRun writes one sorted run, fsyncs it and returns its binding.
func writeRun(path string, recs []Record) (InputBinding, error) {
	f, err := countedOpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return InputBinding{}, err
	}
	h := sha256.New()
	bw := bufio.NewWriter(io.MultiWriter(f, h))
	for i := range recs {
		if _, err := bw.Write(encodeRecord(&recs[i])); err != nil {
			f.Close()
			return InputBinding{}, err
		}
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return InputBinding{}, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return InputBinding{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return InputBinding{}, err
	}
	if err := f.Close(); err != nil {
		return InputBinding{}, err
	}
	return InputBinding{
		File:    filepath.Base(path),
		Size:    st.Size(),
		SHA256:  hex.EncodeToString(h.Sum(nil)),
		Records: int64(len(recs)),
	}, nil
}

func loadRunsManifest(dir string) (*runsManifest, error) {
	var mf runsManifest
	if err := loadJSON(filepath.Join(dir, runsManifestName), &mf); err != nil {
		return nil, fmt.Errorf("load %s (run GenerateRuns first): %w", runsManifestName, err)
	}
	return &mf, nil
}

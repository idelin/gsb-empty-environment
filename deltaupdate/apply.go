package delta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
)

// BlockSource supplies new-file blocks that cannot be reused locally.
type BlockSource interface {
	FetchBlock(index int) ([]byte, error)
}

// Options controls Apply. The function fields are fault-injection hooks
// used by tests to simulate crashes at precise points.
type Options struct {
	Source BlockSource

	// NoJournal disables journal persistence, simulating a progress
	// record that never reached disk.
	NoJournal bool
	// OnBlockDone runs after a block is written and journaled; a non-nil
	// return aborts the apply (simulated crash).
	OnBlockDone func(index int) error
	// AfterAllBlocks runs after all blocks are written but before the
	// whole-file digest is verified.
	AfterAllBlocks func(tmpPath string)
	// BeforeRename runs after the temp file is verified and synced but
	// before it atomically replaces the target.
	BeforeRename func() error
}

type journalFile struct {
	Manifest string `json:"manifest"`
	Done     []bool `json:"done"`
}

// Apply materializes the new file described by p at newPath, reusing
// verified blocks from oldPath and downloading the rest. It is safe to
// interrupt at any point: a rerun re-verifies temp-file content block by
// block and resumes. The old version at newPath is replaced only after
// the whole-file digest matches the manifest.
func Apply(p *Plan, oldPath, newPath string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}
	m := p.Manifest
	if m == nil {
		return errors.New("delta: plan has no manifest")
	}
	tmpPath := newPath + ".part"
	journalPath := newPath + ".journal"

	old, err := ioutil.ReadFile(oldPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		old = nil
	}

	tmp, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}

	// Recover progress. Correctness never depends on the journal: every
	// candidate block is re-verified against the manifest by content.
	done := make([]bool, len(m.Blocks))
	jr, jerr := loadJournal(journalPath)
	if jerr == nil && jr.Manifest == hex.EncodeToString(m.Digest[:]) && len(jr.Done) == len(done) {
		// Journal present: verify only the blocks it claims are done.
		for i, d := range jr.Done {
			if d && verifyBlock(tmp, m, i) {
				done[i] = true
			}
		}
	} else {
		// Journal missing or stale (e.g. confirm written but record
		// lost): recover purely from temp-file content.
		for i := range m.Blocks {
			if verifyBlock(tmp, m, i) {
				done[i] = true
			}
		}
	}

	for _, op := range p.Ops {
		if done[op.Index] {
			continue
		}
		b := m.Blocks[op.Index]
		var data []byte
		if op.Reuse {
			end := op.OldOffset + int64(op.Length)
			if op.OldOffset >= 0 && end <= int64(len(old)) {
				cand := old[op.OldOffset:end]
				if sha256.Sum256(cand) == b.Strong {
					data = append([]byte(nil), cand...)
				}
				// Content mismatch: the old file changed after planning.
				// Fall through and download instead of trusting the offset.
			}
		}
		if data == nil {
			if opts.Source == nil {
				tmp.Close()
				return fmt.Errorf("delta: block %d needs download but no source configured", op.Index)
			}
			d, ferr := opts.Source.FetchBlock(op.Index)
			if ferr != nil {
				tmp.Close()
				return ferr
			}
			if len(d) != op.Length || sha256.Sum256(d) != b.Strong {
				tmp.Close()
				return fmt.Errorf("delta: downloaded block %d failed content verification", op.Index)
			}
			data = d
		}
		if _, err := tmp.WriteAt(data, op.Offset); err != nil {
			tmp.Close()
			return err
		}
		done[op.Index] = true
		if !opts.NoJournal {
			if err := saveJournal(journalPath, m, done); err != nil {
				tmp.Close()
				return err
			}
		}
		if opts.OnBlockDone != nil {
			if err := opts.OnBlockDone(op.Index); err != nil {
				tmp.Close()
				return err
			}
		}
	}

	if opts.AfterAllBlocks != nil {
		opts.AfterAllBlocks(tmpPath)
	}

	// Commit gate: the whole-file digest must pass before anything
	// replaces the old version.
	if err := tmp.Truncate(m.Size); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return err
	}
	h := sha256.New()
	if _, err := io.CopyN(h, tmp, m.Size); err != nil {
		tmp.Close()
		return err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	if sum != m.Digest {
		tmp.Close()
		return errors.New("delta: whole-file digest mismatch; old version left untouched")
	}

	if opts.BeforeRename != nil {
		if err := opts.BeforeRename(); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, newPath); err != nil {
		return err
	}
	syncParentDir(newPath)
	os.Remove(journalPath)
	return nil
}

func verifyBlock(f *os.File, m *Manifest, i int) bool {
	b := m.Blocks[i]
	buf := make([]byte, b.Length)
	if _, err := f.ReadAt(buf, b.Offset); err != nil {
		return false
	}
	return sha256.Sum256(buf) == b.Strong
}

func loadJournal(path string) (*journalFile, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var jf journalFile
	if err := json.Unmarshal(data, &jf); err != nil {
		return nil, err
	}
	return &jf, nil
}

func saveJournal(path string, m *Manifest, done []bool) error {
	jf := journalFile{
		Manifest: hex.EncodeToString(m.Digest[:]),
		Done:     append([]bool(nil), done...),
	}
	data, err := json.Marshal(jf)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
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
	return f.Close()
}

func syncParentDir(path string) {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}

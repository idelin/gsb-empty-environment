package pagefile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Stage identifies a phase of CompactFile for crash-injection hooks.
type Stage int

const (
	// StageWriteDataTemp is reported while the new page file is written to
	// its temporary path; Event.Offset carries bytes written so far.
	StageWriteDataTemp Stage = iota
	// StageWriteTableTemp is reported while the relocation table is written.
	StageWriteTableTemp
	// StageValidate is reported just before on-disk artifacts are validated.
	StageValidate
	// StagePublish is reported just before the atomic renames.
	StagePublish
	// StageDone is reported after a successful publish.
	StageDone
)

func (s Stage) String() string {
	switch s {
	case StageWriteDataTemp:
		return "write-data-temp"
	case StageWriteTableTemp:
		return "write-table-temp"
	case StageValidate:
		return "validate"
	case StagePublish:
		return "publish"
	case StageDone:
		return "done"
	}
	return "unknown"
}

// Event is passed to a Hook. Offset is meaningful for the write stages.
type Event struct {
	Stage  Stage
	Offset int64
}

// Hook observes compaction stages; returning a non-nil error aborts the
// run. Tests use it to simulate crashes (e.g. exiting mid-write).
type Hook func(Event) error

// CompactFile reads the page file at inPath, compacts it, and publishes the
// compacted file to outPath plus the relocation table (JSON) to tablePath.
//
// Publishing is all-or-nothing: both artifacts are written to temporary
// files in the destination directory, fsynced, re-read from disk and
// validated with ValidateEquivalence, and only then renamed into place
// (data first, table last, so the table acts as the commit marker). Any
// failure before the renames leaves previously published outputs untouched
// and removes the temporary files.
func CompactFile(inPath, outPath, tablePath string, hook Hook) (*RelocTable, error) {
	fire := func(e Event) error {
		if hook == nil {
			return nil
		}
		return hook(e)
	}

	src, err := os.Open(inPath)
	if err != nil {
		return nil, err
	}
	oldF, err := Read(src)
	src.Close()
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", inPath, err)
	}

	newF, tbl, err := Compact(oldF)
	if err != nil {
		return nil, fmt.Errorf("compact: %v", err)
	}

	dataTmp := outPath + ".tmp"
	tableTmp := tablePath + ".tmp"
	cleanup := func(err error) (*RelocTable, error) {
		os.Remove(dataTmp)
		os.Remove(tableTmp)
		return nil, err
	}

	if err := writeFileSynced(dataTmp, func(f *os.File) error {
		_, err := WriteTo(f, newF, func(off int64) error {
			return fire(Event{Stage: StageWriteDataTemp, Offset: off})
		})
		return err
	}); err != nil {
		return cleanup(fmt.Errorf("write data temp: %v", err))
	}

	tableJSON, err := json.MarshalIndent(tbl, "", "  ")
	if err != nil {
		return cleanup(err)
	}
	if err := writeFileSynced(tableTmp, func(f *os.File) error {
		if _, err := f.Write(tableJSON); err != nil {
			return err
		}
		return fire(Event{Stage: StageWriteTableTemp, Offset: int64(len(tableJSON))})
	}); err != nil {
		return cleanup(fmt.Errorf("write table temp: %v", err))
	}

	if err := fire(Event{Stage: StageValidate, Offset: -1}); err != nil {
		return cleanup(err)
	}
	// Validate the artifacts as they sit on disk, not the in-memory copies.
	diskF, err := readFile(dataTmp)
	if err != nil {
		return cleanup(fmt.Errorf("re-read data temp: %v", err))
	}
	diskTbl, err := readTable(tableTmp)
	if err != nil {
		return cleanup(fmt.Errorf("re-read table temp: %v", err))
	}
	if err := ValidateEquivalence(oldF, diskF, diskTbl); err != nil {
		return cleanup(fmt.Errorf("validation failed, nothing published: %v", err))
	}

	if err := fire(Event{Stage: StagePublish, Offset: -1}); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(dataTmp, outPath); err != nil {
		return cleanup(fmt.Errorf("publish data: %v", err))
	}
	if err := os.Rename(tableTmp, tablePath); err != nil {
		os.Remove(outPath)
		return cleanup(fmt.Errorf("publish table: %v", err))
	}
	syncDir(filepath.Dir(outPath))

	if err := fire(Event{Stage: StageDone, Offset: -1}); err != nil {
		return nil, err
	}
	return tbl, nil
}

func writeFileSynced(path string, write func(*os.File) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	werr := write(f)
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if serr != nil {
		return serr
	}
	return cerr
}

func readFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

func readTable(path string) (*RelocTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tbl RelocTable
	if err := json.Unmarshal(raw, &tbl); err != nil {
		return nil, err
	}
	return &tbl, nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

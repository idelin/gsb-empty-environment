package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SegmentStatus records the verified state of one manifest segment.
type SegmentStatus struct {
	Name     string `json:"name"`
	Present  bool   `json:"present"`
	SizeOK   bool   `json:"size_ok"`
	DigestOK bool   `json:"digest_ok"`
	Detail   string `json:"detail,omitempty"`
}

func (s SegmentStatus) OK() bool { return s.Present && s.SizeOK && s.DigestOK }

// Report is the result of the read-only audit. Audit never opens any
// file for writing; it only reads and hashes.
type Report struct {
	Dir             string          `json:"dir"`
	ManifestOK      bool            `json:"manifest_ok"`
	ManifestDetail  string          `json:"manifest_detail,omitempty"`
	Segments        []SegmentStatus `json:"segments"`
	OrphanSegments  []string        `json:"orphan_segments,omitempty"`
	IndexPresent    bool            `json:"index_present"`
	IndexParseOK    bool            `json:"index_parse_ok"`
	IndexConsistent bool            `json:"index_consistent"`
	IndexProblems   []string        `json:"index_problems,omitempty"`
	Summary         string          `json:"summary"`
}

// Audit inspects dir read-only and returns a report plus an executable
// repair plan. It never modifies, creates, or deletes any file.
func Audit(dir string) (*Report, *Plan, error) {
	rep := &Report{Dir: dir}
	var m Manifest
	manifestBytes, merr := os.ReadFile(filepath.Join(dir, ManifestName))
	if merr != nil {
		rep.ManifestDetail = merr.Error()
	} else if err := jsonUnmarshal(manifestBytes, &m); err != nil {
		rep.ManifestDetail = err.Error()
	} else {
		rep.ManifestOK = true
	}

	// 1. Verify every manifest segment against disk.
	inManifest := map[string]bool{}
	if rep.ManifestOK {
		for _, ms := range m.Segments {
			inManifest[ms.Name] = true
			st := SegmentStatus{Name: ms.Name}
			sum, size, err := sha256File(filepath.Join(dir, ms.Name))
			switch {
			case os.IsNotExist(err):
				st.Detail = "missing"
			case err != nil:
				st.Detail = err.Error()
			default:
				st.Present = true
				st.SizeOK = size == ms.Size
				st.DigestOK = sum == ms.SHA256
				if !st.SizeOK {
					st.Detail = fmt.Sprintf("size %d, manifest says %d", size, ms.Size)
				} else if !st.DigestOK {
					st.Detail = "sha256 mismatch"
				}
			}
			rep.Segments = append(rep.Segments, st)
		}
	}

	// 2. Report segment files on disk that the manifest does not know.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, "seg-") && strings.HasSuffix(name, ".dat") && !inManifest[name] {
			rep.OrphanSegments = append(rep.OrphanSegments, name)
		}
	}
	sort.Strings(rep.OrphanSegments)

	// 3. Verify the index against the manifest and segment contents.
	indexPath := filepath.Join(dir, IndexName)
	indexBytes, ierr := os.ReadFile(indexPath)
	var idx Index
	if os.IsNotExist(ierr) {
		rep.IndexProblems = append(rep.IndexProblems, "index missing")
	} else if ierr != nil {
		rep.IndexProblems = append(rep.IndexProblems, ierr.Error())
	} else {
		rep.IndexPresent = true
		if err := jsonUnmarshal(indexBytes, &idx); err != nil {
			rep.IndexProblems = append(rep.IndexProblems, err.Error())
		} else {
			rep.IndexParseOK = true
			rep.IndexProblems = append(rep.IndexProblems, checkIndex(dir, &m, &idx)...)
		}
	}
	rep.IndexConsistent = rep.IndexPresent && rep.IndexParseOK && len(rep.IndexProblems) == 0

	// 4. State summary: binds manifest bytes, per-segment verification
	// results, index bytes and a full file inventory into one digest.
	// Apply re-verifies it before touching anything, so any change to
	// the directory since the audit aborts the run.
	inv, err := inventory(dir)
	if err != nil {
		return nil, nil, err
	}
	rep.Summary = computeSummary(manifestBytes, merr == nil, rep.Segments, indexBytes, rep.IndexPresent, inv)

	plan := buildPlan(rep)
	return rep, plan, nil
}

// inventory hashes every regular file in dir (except the apply journal
// and temp files) so the summary binds the complete directory state,
// including orphan segments the manifest does not mention.
func inventory(dir string) (string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, e := range ents {
		name := e.Name()
		if name == JournalName || strings.HasPrefix(name, tmpPrefix) || !e.Type().IsRegular() {
			continue
		}
		sum, size, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s:%d:%s", name, size, sum))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

// checkIndex cross-checks a parsed index against manifest and segments.
func checkIndex(dir string, m *Manifest, idx *Index) []string {
	var problems []string
	for _, ms := range m.Segments {
		is, ok := idx.Segments[ms.Name]
		if !ok {
			problems = append(problems, "index lacks segment "+ms.Name)
			continue
		}
		if is.Size != ms.Size || is.SHA256 != ms.SHA256 {
			problems = append(problems, "index digest/size mismatch for "+ms.Name)
		}
	}
	for name := range idx.Segments {
		found := false
		for _, ms := range m.Segments {
			if ms.Name == name {
				found = true
			}
		}
		if !found {
			problems = append(problems, "index references unknown segment "+name)
		}
	}
	// Spot-check every entry: the record at the referenced offset must
	// decode and its key must match. Read-only.
	segHandles := map[string]*os.File{}
	defer func() {
		for _, f := range segHandles {
			f.Close()
		}
	}()
	segSizes := map[string]int64{}
	for _, ms := range m.Segments {
		segSizes[ms.Name] = ms.Size
	}
	for _, key := range sortedKeys(idx.Entries) {
		e := idx.Entries[key]
		size, known := segSizes[e.Segment]
		if !known {
			problems = append(problems, fmt.Sprintf("entry %q in unknown segment %s", key, e.Segment))
			continue
		}
		if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > size {
			problems = append(problems, fmt.Sprintf("entry %q out of bounds", key))
			continue
		}
		f, ok := segHandles[e.Segment]
		if !ok {
			var err error
			f, err = os.Open(filepath.Join(dir, e.Segment))
			if err != nil {
				problems = append(problems, fmt.Sprintf("entry %q: %v", key, err))
				continue
			}
			segHandles[e.Segment] = f
		}
		rkey, _, length, err := decodeRecordAt(f, e.Offset)
		if err != nil {
			problems = append(problems, fmt.Sprintf("entry %q: %v", key, err))
			continue
		}
		if string(rkey) != key || length != e.Length {
			problems = append(problems, fmt.Sprintf("entry %q points at wrong record", key))
		}
	}
	sort.Strings(problems)
	return problems
}

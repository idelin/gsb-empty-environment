package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Code classifies a finding. Missing files, bad digests and unknown
// versions are reported as distinct codes.
type Code string

const (
	CodeMissingFile  Code = "MISSING_FILE"         // declared but never sealed
	CodeEmptyFile    Code = "EMPTY_FILE"           // declared size 0, digest of empty content (informational)
	CodeBadDigest    Code = "BAD_DIGEST"           // content does not match the pinned digest
	CodeUnknownVer   Code = "UNKNOWN_VERSION"      // chunk or verify for a version with no accepted manifest
	CodeUnknownFile  Code = "UNKNOWN_FILE"         // chunk for a file not present in the manifest
	CodeTimeRange    Code = "TIME_RANGE_MISMATCH"  // record timestamps outside the declared range
	CodeDanglingRef  Code = "DANGLING_REFERENCE"   // cross-file reference to a record that does not exist
	CodeNameConflict Code = "NAME_DIGEST_CONFLICT" // same file name pinned to different digests
	CodeMalformedRec Code = "MALFORMED_RECORD"     // record line cannot be parsed
)

// Finding is one verification problem, located as precisely as possible.
type Finding struct {
	Code    Code
	Version string
	File    string
	Record  string // source record id for DANGLING_REFERENCE
	Detail  string
}

func (f Finding) String() string {
	loc := f.File
	if f.Record != "" {
		loc += "#" + f.Record
	}
	return fmt.Sprintf("%s version=%q %s: %s", f.Code, f.Version, loc, f.Detail)
}

// Report is the outcome of verifying one manifest version.
type Report struct {
	Version  string
	Findings []Finding
}

// OK reports whether the package is fully valid. EMPTY_FILE is
// informational and does not fail the report.
func (r Report) OK() bool {
	for _, f := range r.Findings {
		if f.Code != CodeEmptyFile {
			return false
		}
	}
	return true
}

var emptyDigest = digestOf(nil)

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Verifier incrementally ingests versioned manifests and chunks, and
// verifies evidence packages. It never modifies its inputs: chunks are
// copied on arrival and sealed content is stored separately.
type Verifier struct {
	st  *store
	tel *Telemetry

	mu        sync.Mutex
	manifests map[string]Manifest
	ingest    []Finding // findings raised during ingestion
}

// NewVerifier creates a verifier. If dir is non-empty, sealed files are
// persisted there and recovered on restart; incomplete chunk buffers
// are volatile and must be retransmitted after a crash.
func NewVerifier(dir string) (*Verifier, error) {
	st, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	return &Verifier{
		st:        st,
		tel:       &Telemetry{},
		manifests: map[string]Manifest{},
	}, nil
}

// Telemetry exposes the locally synthesized event log.
func (v *Verifier) Telemetry() *Telemetry { return v.tel }

// AcceptManifest registers a versioned manifest. A manifest that pins
// the same file name to two different digests is rejected. Accepting
// the same version twice is only allowed with identical content.
func (v *Verifier) AcceptManifest(m Manifest) error {
	if m.Version == "" {
		return errors.New("manifest version is empty")
	}
	byName := map[string]FileEntry{}
	for _, e := range m.Files {
		if prev, dup := byName[e.Name]; dup && prev.SHA256 != e.SHA256 {
			v.addIngest(Finding{
				Code:    CodeNameConflict,
				Version: m.Version,
				File:    e.Name,
				Detail:  fmt.Sprintf("name pinned to digests %s and %s", prev.SHA256, e.SHA256),
			})
			v.tel.emit("manifest_rejected", m.Version+" "+e.Name)
			return fmt.Errorf("manifest %q: name %q pinned to conflicting digests", m.Version, e.Name)
		}
		byName[e.Name] = e
	}
	files := make([]FileEntry, 0, len(byName))
	for _, e := range byName {
		files = append(files, e)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	norm := Manifest{Version: m.Version, Files: files}

	v.mu.Lock()
	defer v.mu.Unlock()
	if prev, seen := v.manifests[m.Version]; seen {
		if fmt.Sprint(prev) != fmt.Sprint(norm) {
			return fmt.Errorf("manifest version %q already accepted with different content", m.Version)
		}
		return nil // idempotent redelivery
	}
	v.manifests[m.Version] = norm
	v.tel.emit("manifest_accepted", m.Version)
	return nil
}

// AcceptChunk ingests one chunk of evidence bytes. Chunks are bound to
// the manifest version they arrive under; commit ("签收") is atomic:
// a file becomes sealed only when every byte up to the declared size is
// present and the digest matches the entry pinned by that version.
func (v *Verifier) AcceptChunk(version, name string, offset int64, data []byte) {
	v.mu.Lock()
	m, known := v.manifests[version]
	v.mu.Unlock()
	if !known {
		v.addIngest(Finding{Code: CodeUnknownVer, Version: version, File: name,
			Detail: "chunk for version with no accepted manifest; chunk rejected"})
		v.tel.emit("chunk_rejected", version+" "+name)
		return
	}
	var entry *FileEntry
	for i := range m.Files {
		if m.Files[i].Name == name {
			entry = &m.Files[i]
			break
		}
	}
	if entry == nil {
		v.addIngest(Finding{Code: CodeUnknownFile, Version: version, File: name,
			Detail: "chunk for file not listed in manifest; chunk rejected"})
		v.tel.emit("chunk_rejected", version+" "+name)
		return
	}

	v.st.mu.Lock()
	defer v.st.mu.Unlock()
	if _, done := v.st.get(version, name); done {
		return // already sealed; late chunks are ignored
	}
	buf := v.st.bufferFor(version, name)
	buf.add(offset, data)
	image := buf.assemble(entry.Size)
	if image == nil {
		v.tel.emit("chunk_buffered", fmt.Sprintf("%s %s offset=%d", version, name, offset))
		return
	}
	if got := digestOf(image); got != entry.SHA256 {
		v.st.drop(version, name)
		v.addIngest(Finding{Code: CodeBadDigest, Version: version, File: name,
			Detail: fmt.Sprintf("assembled digest %s, manifest pins %s", got, entry.SHA256)})
		v.tel.emit("digest_mismatch", version+" "+name)
		return
	}
	if err := v.st.seal(version, name, image); err != nil {
		v.addIngest(Finding{Code: CodeBadDigest, Version: version, File: name,
			Detail: "seal failed: " + err.Error()})
		return
	}
	v.tel.emit("file_sealed", version+" "+name)
}

func (v *Verifier) addIngest(f Finding) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ingest = append(v.ingest, f)
}

// Verify runs a full verification of one manifest version. The result
// is independent of chunk arrival order and of whether files were
// sealed in this process or recovered from the store directory.
func (v *Verifier) Verify(version string) Report {
	v.mu.Lock()
	m, known := v.manifests[version]
	ingest := append([]Finding(nil), v.ingest...)
	v.mu.Unlock()

	rep := Report{Version: version}
	if !known {
		rep.Findings = append(rep.Findings, Finding{Code: CodeUnknownVer, Version: version,
			Detail: "no accepted manifest for this version"})
		for _, f := range ingest {
			if f.Version == version {
				rep.Findings = append(rep.Findings, f)
			}
		}
		return rep
	}

	v.st.mu.Lock()
	sealed := map[string][]byte{}
	for _, e := range m.Files {
		if data, ok := v.st.get(version, e.Name); ok {
			sealed[e.Name] = data
		}
	}
	v.st.mu.Unlock()

	verified := map[string][]Record{} // files whose digest re-check passed
	for _, e := range m.Files {
		data, ok := sealed[e.Name]
		if !ok {
			if e.Size == 0 && e.SHA256 == emptyDigest {
				rep.Findings = append(rep.Findings, Finding{Code: CodeEmptyFile,
					Version: version, File: e.Name,
					Detail: "declared empty file; digest matches empty content"})
				verified[e.Name] = nil
			} else {
				rep.Findings = append(rep.Findings, Finding{Code: CodeMissingFile,
					Version: version, File: e.Name,
					Detail: fmt.Sprintf("declared size=%d sha256=%s but no sealed content", e.Size, e.SHA256)})
			}
			continue
		}
		if got := digestOf(data); got != e.SHA256 {
			rep.Findings = append(rep.Findings, Finding{Code: CodeBadDigest,
				Version: version, File: e.Name,
				Detail: fmt.Sprintf("sealed digest %s, manifest pins %s", got, e.SHA256)})
			continue
		}
		recs, err := parseRecords(data)
		if err != nil {
			rep.Findings = append(rep.Findings, Finding{Code: CodeMalformedRec,
				Version: version, File: e.Name, Detail: err.Error()})
			continue
		}
		verified[e.Name] = recs
		if !e.Start.IsZero() && !e.End.IsZero() {
			for _, r := range recs {
				if r.TS.Before(e.Start) || r.TS.After(e.End) {
					rep.Findings = append(rep.Findings, Finding{Code: CodeTimeRange,
						Version: version, File: e.Name, Record: r.ID,
						Detail: fmt.Sprintf("record ts %s outside declared [%s, %s]",
							r.TS.Format("15:04:05"), e.Start.Format("15:04:05"), e.End.Format("15:04:05"))})
				}
			}
		}
	}

	// Cross-file references are only evaluated when both endpoints are
	// digest-verified under this manifest version.
	for _, e := range m.Files {
		recs, ok := verified[e.Name]
		if !ok || e.Kind != KindTrace {
			continue
		}
		for _, r := range recs {
			for _, ref := range r.Refs {
				targetRecs, targetOK := verified[ref.File]
				if !targetOK {
					continue // target file not verified; its own finding covers it
				}
				found := false
				for _, tr := range targetRecs {
					if tr.ID == ref.Record {
						found = true
						break
					}
				}
				if !found {
					rep.Findings = append(rep.Findings, Finding{Code: CodeDanglingRef,
						Version: version, File: e.Name, Record: r.ID,
						Detail: fmt.Sprintf("references %s:%s which does not exist", ref.File, ref.Record)})
				}
			}
		}
	}

	for _, f := range ingest {
		if f.Version == version {
			rep.Findings = append(rep.Findings, f)
		}
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Record != b.Record {
			return a.Record < b.Record
		}
		return a.Code < b.Code
	})
	return rep
}

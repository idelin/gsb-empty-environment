package verifier

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Chunk is one incrementally arriving piece of evidence, tagged with the
// manifest version it belongs to. Chunks of different versions are kept in
// strictly separate staging areas, so old chunks can never be spliced into
// a newer manifest.
type Chunk struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	Data    []byte `json:"data"`
}

// Digest computes the content digest of the chunk payload.
func (c Chunk) Digest() string {
	sum := sha256.Sum256(c.Data)
	return hex.EncodeToString(sum[:])
}

// Verifier ingests chunks, signs off versioned manifests atomically and
// recovers its state from a local directory. It is safe for concurrent use.
type Verifier struct {
	dir    string
	em     Emitter
	mu     sync.Mutex
	staged map[string]map[string]chunkMeta // version -> name -> meta
	sealed map[string]sealedRecord         // version -> sealed record
}

// Open creates or recovers a verifier rooted at dir.
func Open(dir string, em Emitter) (*Verifier, error) {
	if em == nil {
		em = nopEmitter{}
	}
	v := &Verifier{
		dir:    dir,
		em:     em,
		staged: map[string]map[string]chunkMeta{},
		sealed: map[string]sealedRecord{},
	}
	if err := os.MkdirAll(filepath.Join(dir, "staging"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(v.sealedDir(), 0o755); err != nil {
		return nil, err
	}
	if err := v.recover(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *Verifier) emit(typ, version, file, detail string) {
	v.em.Emit(Event{Type: typ, Version: version, File: file, Detail: detail})
}

// StageChunk stores one arriving chunk. It is idempotent for identical
// redelivery, rejects a same-named chunk with a different digest, and
// refuses chunks for versions that are already sealed.
func (v *Verifier) StageChunk(c Chunk) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, ok := v.sealed[c.Version]; ok {
		iss := Issue{
			Category: CatUnknownVersion,
			Version:  c.Version,
			File:     c.Name,
			Detail:   "version already sealed; refusing to splice a chunk into a sealed manifest",
		}
		v.emit(EventChunkRejected, c.Version, c.Name, iss.Detail)
		return &Rejection{Issue: iss}
	}

	meta := chunkMeta{Name: c.Name, Digest: c.Digest(), Size: int64(len(c.Data))}
	if prev, ok := v.staged[c.Version][c.Name]; ok {
		if prev.Digest != meta.Digest {
			iss := Issue{
				Category: CatNameConflict,
				Version:  c.Version,
				File:     c.Name,
				Detail:   fmt.Sprintf("same file name arrived with a different digest (staged %s, new %s)", prev.Digest, meta.Digest),
			}
			v.emit(EventChunkRejected, c.Version, c.Name, iss.Detail)
			return &Rejection{Issue: iss}
		}
		return nil // identical redelivery
	}

	if err := v.persistChunk(c.Version, meta, c.Data); err != nil {
		return err
	}
	if v.staged[c.Version] == nil {
		v.staged[c.Version] = map[string]chunkMeta{}
	}
	v.staged[c.Version][c.Name] = meta
	v.emit(EventChunkStaged, c.Version, c.Name, fmt.Sprintf("staged %d bytes, sha256=%s", meta.Size, meta.Digest))
	return nil
}

// Commit attempts atomic sign-off of a manifest. Either every check passes
// and the version is sealed, or the commit is rejected as a whole and
// staging is left untouched so late chunks can still arrive and the commit
// can be retried. Committing an identical manifest twice is idempotent;
// committing a different manifest under a sealed version is an
// unknown-version rejection.
func (v *Verifier) Commit(m Manifest) (*Report, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	md := m.Digest()
	if rec, ok := v.sealed[m.Version]; ok {
		if rec.ManifestDigest == md {
			rep := rec.Report
			return &rep, nil
		}
		iss := Issue{
			Category: CatUnknownVersion,
			Version:  m.Version,
			Detail:   fmt.Sprintf("conflicting manifest for sealed version (sealed %s, new %s)", rec.ManifestDigest, md),
		}
		v.emit(EventCommitRejected, m.Version, "", iss.Detail)
		return nil, &Rejection{Issue: iss}
	}

	v.emit(EventCommitStarted, m.Version, "", fmt.Sprintf("manifest digest %s, %d file(s), %d reference(s)", md, len(m.Files), len(m.References)))
	rep := v.check(m)
	rep.ManifestDigest = md

	if len(rep.Issues) == 0 {
		rep.Status = "sealed"
		rec := sealedRecord{ManifestDigest: md, Manifest: m, Report: *rep}
		if err := v.persistSealed(rec); err != nil {
			return nil, err
		}
		if err := v.dropStaging(m.Version); err != nil {
			return nil, err
		}
		delete(v.staged, m.Version)
		v.sealed[m.Version] = rec
		v.emit(EventCommitSealed, m.Version, "", "all checks passed; version sealed atomically")
	} else {
		rep.Status = "rejected"
		sort.SliceStable(rep.Issues, func(i, j int) bool { return rep.Issues[i].Category < rep.Issues[j].Category })
		for _, iss := range rep.Issues {
			v.emit(EventIssueFound, iss.Version, iss.File, string(iss.Category)+": "+iss.Detail)
		}
		v.emit(EventCommitRejected, m.Version, "", fmt.Sprintf("%d issue(s); nothing sealed, staging preserved", len(rep.Issues)))
	}
	return rep, nil
}

// check runs every verification pass against the staged chunks of the
// manifest's own version. It performs no mutation.
func (v *Verifier) check(m Manifest) *Report {
	rep := &Report{Version: m.Version}
	fileOK := map[string]bool{}
	records := map[string][]Record{}

	for _, fe := range m.Files {
		meta, ok := v.staged[m.Version][fe.Name]
		if !ok {
			detail := "declared in manifest but no chunk arrived"
			if fe.Size == 0 {
				detail = "declared as an empty file but even the empty chunk never arrived"
			}
			rep.Issues = append(rep.Issues, Issue{Category: CatMissingFile, Version: m.Version, File: fe.Name, Detail: detail})
			continue
		}
		data, err := v.loadChunk(m.Version, fe.Name)
		if err != nil {
			rep.Issues = append(rep.Issues, Issue{Category: CatMissingFile, Version: m.Version, File: fe.Name, Detail: "staged payload unreadable: " + err.Error()})
			continue
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != fe.SHA256 || meta.Digest != fe.SHA256 {
			rep.Issues = append(rep.Issues, Issue{
				Category: CatBadDigest, Version: m.Version, File: fe.Name,
				Detail: fmt.Sprintf("manifest declares sha256=%s but payload hashes to %s", fe.SHA256, got),
			})
			continue
		}
		if int64(len(data)) != fe.Size {
			rep.Issues = append(rep.Issues, Issue{
				Category: CatSizeMismatch, Version: m.Version, File: fe.Name,
				Detail: fmt.Sprintf("manifest declares %d bytes, payload has %d", fe.Size, len(data)),
			})
			continue
		}
		recs, err := parseRecords(data)
		if err != nil {
			rep.Issues = append(rep.Issues, Issue{Category: CatParseError, Version: m.Version, File: fe.Name, Detail: err.Error()})
			continue
		}
		records[fe.Name] = recs
		fr := FileResult{Name: fe.Name, Kind: fe.Kind, Records: len(recs), Empty: len(data) == 0}
		if len(recs) > 0 {
			fr.Actual = actualRange(recs)
			if !fe.TimeRange.Contains(fr.Actual.Start) || !fe.TimeRange.Contains(fr.Actual.End) {
				rep.Issues = append(rep.Issues, Issue{
					Category: CatTimeRange, Version: m.Version, File: fe.Name,
					Detail: fmt.Sprintf("records span [%s, %s] outside declared [%s, %s]",
						fr.Actual.Start.Format("15:04:05"), fr.Actual.End.Format("15:04:05"),
						fe.TimeRange.Start.Format("15:04:05"), fe.TimeRange.End.Format("15:04:05")),
				})
				continue
			}
		}
		fileOK[fe.Name] = true
		rep.Files = append(rep.Files, fr)
	}

	for name := range v.staged[m.Version] {
		if _, declared := m.Entry(name); !declared {
			rep.Issues = append(rep.Issues, Issue{
				Category: CatUnexpectedFile, Version: m.Version, File: name,
				Detail: "chunk staged under this version but not declared by the manifest",
			})
		}
	}

	// Cross-file references hold only after the target file of this version
	// has itself passed digest, size and time-range verification.
	for _, ref := range m.References {
		if !fileOK[ref.Target.File] {
			rep.Issues = append(rep.Issues, Issue{
				Category: CatReferenceUnverified, Version: m.Version,
				File: ref.Source.File, Record: ref.Source.Record,
				TargetFile: ref.Target.File, TargetRecord: ref.Target.Record,
				Detail: "target file of this version is not verified, so the reference cannot hold",
			})
			continue
		}
		if !fileOK[ref.Source.File] {
			rep.Issues = append(rep.Issues, Issue{
				Category: CatReferenceUnverified, Version: m.Version,
				File: ref.Source.File, Record: ref.Source.Record,
				TargetFile: ref.Target.File, TargetRecord: ref.Target.Record,
				Detail: "source file of this version is not verified, so the reference cannot hold",
			})
			continue
		}
		src := findRecord(records[ref.Source.File], ref.Source.Record)
		if src == nil {
			rep.Issues = append(rep.Issues, Issue{
				Category: CatDanglingReference, Version: m.Version,
				File: ref.Source.File, Record: ref.Source.Record,
				TargetFile: ref.Target.File, TargetRecord: ref.Target.Record,
				Detail: "source record does not exist in its own file",
			})
			continue
		}
		tgt := findRecord(records[ref.Target.File], ref.Target.Record)
		if tgt == nil {
			rep.Issues = append(rep.Issues, Issue{
				Category: CatDanglingReference, Version: m.Version,
				File: ref.Source.File, Record: ref.Source.Record,
				TargetFile: ref.Target.File, TargetRecord: ref.Target.Record,
				Detail: fmt.Sprintf("record %q in %s cites record %q in %s, which does not exist",
					ref.Source.Record, ref.Source.File, ref.Target.Record, ref.Target.File),
			})
			continue
		}
		if span := src.SpanRange(); !span.Contains(tgt.Timestamp) {
			rep.Issues = append(rep.Issues, Issue{
				Category: CatTimeRange, Version: m.Version,
				File: ref.Source.File, Record: ref.Source.Record,
				TargetFile: ref.Target.File, TargetRecord: ref.Target.Record,
				Detail: fmt.Sprintf("referenced record timestamp %s lies outside source span [%s, %s]",
					tgt.Timestamp.Format("15:04:05.000"), span.Start.Format("15:04:05.000"), span.End.Format("15:04:05.000")),
			})
		}
	}
	return rep
}

// parseRecords decodes a JSONL evidence file. An empty payload yields zero
// records and no error, keeping empty files distinct from missing ones.
func parseRecords(data []byte) ([]Record, error) {
	var recs []Record
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if r.ID == "" {
			return nil, fmt.Errorf("line %d: record has no id", i+1)
		}
		recs = append(recs, r)
	}
	return recs, nil
}

func actualRange(recs []Record) TimeRange {
	r := recs[0].SpanRange()
	for _, rec := range recs[1:] {
		s := rec.SpanRange()
		if s.Start.Before(r.Start) {
			r.Start = s.Start
		}
		if s.End.After(r.End) {
			r.End = s.End
		}
	}
	return r
}

func findRecord(recs []Record, id string) *Record {
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i]
		}
	}
	return nil
}

// Status reports which versions are staged and which are sealed.
func (v *Verifier) Status() (staged map[string][]string, sealed []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	staged = map[string][]string{}
	for ver, files := range v.staged {
		for name := range files {
			staged[ver] = append(staged[ver], name)
		}
		sort.Strings(staged[ver])
	}
	for ver := range v.sealed {
		sealed = append(sealed, ver)
	}
	sort.Strings(sealed)
	return staged, sealed
}

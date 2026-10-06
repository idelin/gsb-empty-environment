package diagnostics

import (
	"errors"
	"sort"
)

// ErrFutureVersion is returned by File.Submit when a stage reports results
// for an analysis version newer than the file's current version.
var ErrFutureVersion = errors.New("diagnostics: analysis version is newer than the current file version")

// contribution is one stage's latest result set, bound to the analysis
// version it was computed from.
type contribution struct {
	version uint64
	diags   []Diagnostic
}

// File tracks every stage's diagnostic contribution for one file, plus the
// edit history needed to map old ranges forward. The zero value is ready
// to use.
type File struct {
	version uint64
	edits   map[uint64][]Edit // edits[v] transforms version v into v+1
	stages  map[string]contribution
}

// NewFile returns an empty File at version 0.
func NewFile() *File {
	return &File{
		edits:  make(map[uint64][]Edit),
		stages: make(map[string]contribution),
	}
}

// Version returns the file's current version.
func (f *File) Version() uint64 { return f.version }

// ApplyEdits records one batch of edits and advances the file version by
// one. Within the batch, each edit's offsets are expressed in the
// coordinates produced by the previous edit. Diagnostics anchored to
// content the batch touches become invalid; all others keep their identity
// and are re-anchored to the new coordinates on read.
func (f *File) ApplyEdits(edits ...Edit) {
	f.lazyInit()
	f.edits[f.version] = append([]Edit(nil), edits...)
	f.version++
}

// Submit replaces stage's entire contribution with diags, which must have
// been computed against analysis version version. Contributions from other
// stages are untouched. An empty diags slice revokes the stage's
// contribution. Stale results (version older than the current version) are
// accepted and mapped forward through the recorded edit chain; results
// from the future are rejected with ErrFutureVersion.
func (f *File) Submit(stage string, version uint64, diags []Diagnostic) error {
	if version > f.version {
		return ErrFutureVersion
	}
	f.lazyInit()
	if len(diags) == 0 {
		delete(f.stages, stage)
		return nil
	}
	f.stages[stage] = contribution{
		version: version,
		diags:   append([]Diagnostic(nil), diags...),
	}
	return nil
}

// Revoke removes every diagnostic contributed by stage.
func (f *File) Revoke(stage string) {
	f.lazyInit()
	delete(f.stages, stage)
}

func (f *File) lazyInit() {
	if f.edits == nil {
		f.edits = make(map[uint64][]Edit)
	}
	if f.stages == nil {
		f.stages = make(map[string]contribution)
	}
}

// Merged is the merged view of one logical diagnostic: every contribution
// sharing the same identity, with ranges mapped to the current version.
type Merged struct {
	Identity Identity
	Current  Range    // range in current-version coordinates
	Severity Severity // maximum severity across contributions
	Stages   []string // contributing stages, sorted
	Messages []string // distinct messages, sorted
}

// Diagnostics returns the merged, currently valid diagnostics in
// deterministic order: by current range, then by identity. Diagnostics
// whose content was touched by an edit are omitted; contributions are
// never deduplicated by current text range alone — only identical
// identities merge.
func (f *File) Diagnostics() []Merged {
	type accumulator struct {
		current  Range
		severity Severity
		stages   []string
		messages []string
	}
	merged := make(map[Identity]*accumulator)
	for stage, c := range f.stages {
		for _, d := range c.diags {
			current, ok := f.mapRange(c.version, d.Range)
			if !ok {
				continue
			}
			id := Identity{Version: c.version, Range: d.Range, RuleID: d.RuleID}
			acc := merged[id]
			if acc == nil {
				acc = &accumulator{current: current}
				merged[id] = acc
			}
			if d.Severity > acc.severity {
				acc.severity = d.Severity
			}
			acc.stages = appendIfMissing(acc.stages, stage)
			acc.messages = appendIfMissing(acc.messages, d.Message)
		}
	}
	out := make([]Merged, 0, len(merged))
	for id, acc := range merged {
		sort.Strings(acc.stages)
		sort.Strings(acc.messages)
		out = append(out, Merged{
			Identity: id,
			Current:  acc.current,
			Severity: acc.severity,
			Stages:   acc.stages,
			Messages: acc.messages,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Current.Start != b.Current.Start {
			return a.Current.Start < b.Current.Start
		}
		if a.Current.End != b.Current.End {
			return a.Current.End < b.Current.End
		}
		if a.Identity.Version != b.Identity.Version {
			return a.Identity.Version < b.Identity.Version
		}
		if a.Identity.Range.Start != b.Identity.Range.Start {
			return a.Identity.Range.Start < b.Identity.Range.Start
		}
		if a.Identity.Range.End != b.Identity.Range.End {
			return a.Identity.Range.End < b.Identity.Range.End
		}
		return a.Identity.RuleID < b.Identity.RuleID
	})
	return out
}

// mapRange maps r from the coordinates of version from to the current
// version, following the recorded edit chain.
func (f *File) mapRange(from uint64, r Range) (Range, bool) {
	for v := from; v < f.version; v++ {
		var ok bool
		r, ok = MapThroughEdits(r, f.edits[v])
		if !ok {
			return Range{}, false
		}
	}
	return r, true
}

func appendIfMissing(list []string, s string) []string {
	for _, existing := range list {
		if existing == s {
			return list
		}
	}
	return append(list, s)
}

// Store tracks diagnostics for many files, each with its own version
// counter and edit history.
type Store struct {
	files map[string]*File
}

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{files: make(map[string]*File)} }

// File returns the diagnostics state for path, creating it on first use.
func (s *Store) File(path string) *File {
	f := s.files[path]
	if f == nil {
		f = NewFile()
		s.files[path] = f
	}
	return f
}

// Remove discards all diagnostics and history for path.
func (s *Store) Remove(path string) { delete(s.files, path) }

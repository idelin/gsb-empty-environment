// Package verifier implements an evidence-bundle verifier for trace, log and
// metric packages. It supports incremental chunk arrival, versioned
// manifests, atomic sign-off, crash recovery and cross-file reference
// validation. It never modifies its inputs.
package verifier

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Kind classifies an evidence file.
type Kind string

const (
	KindTrace  Kind = "trace"
	KindLog    Kind = "log"
	KindMetric Kind = "metric"
)

// TimeRange is an inclusive [Start, End] window declared by a manifest.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Contains reports whether t falls inside the range.
func (r TimeRange) Contains(t time.Time) bool {
	return !t.Before(r.Start) && !t.After(r.End)
}

// FileEntry declares one evidence file inside a manifest.
type FileEntry struct {
	Name      string    `json:"name"`
	Kind      Kind      `json:"kind"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	TimeRange TimeRange `json:"timeRange"`
}

// RecordRef points at one record inside one evidence file.
type RecordRef struct {
	File   string `json:"file"`
	Record string `json:"record"`
}

// Reference is a cross-file link, e.g. a trace span citing a log record.
type Reference struct {
	Source RecordRef `json:"source"`
	Target RecordRef `json:"target"`
}

// Manifest is the versioned inventory of an evidence bundle.
type Manifest struct {
	Version    string      `json:"version"`
	Files      []FileEntry `json:"files"`
	References []Reference `json:"references"`
}

// Digest returns the canonical content digest of the manifest.
func (m Manifest) Digest() string {
	raw, _ := json.Marshal(m) // struct field order is deterministic
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Entry looks up a file declaration by name.
func (m Manifest) Entry(name string) (FileEntry, bool) {
	for _, fe := range m.Files {
		if fe.Name == name {
			return fe, true
		}
	}
	return FileEntry{}, false
}

// Record is one line of an evidence file (JSONL).
type Record struct {
	ID        string     `json:"id"`
	Timestamp time.Time  `json:"timestamp"`
	Start     *time.Time `json:"start,omitempty"`
	End       *time.Time `json:"end,omitempty"`
}

// SpanRange returns the effective time window of a record.
func (r Record) SpanRange() TimeRange {
	if r.Start != nil && r.End != nil {
		return TimeRange{Start: *r.Start, End: *r.End}
	}
	return TimeRange{Start: r.Timestamp, End: r.Timestamp}
}

// Category classifies a verification finding. Distinct categories are used
// for missing files, bad digests and unknown versions so callers can tell
// them apart without parsing messages.
type Category string

const (
	CatMissingFile         Category = "missing_file"
	CatBadDigest           Category = "bad_digest"
	CatSizeMismatch        Category = "size_mismatch"
	CatUnknownVersion      Category = "unknown_version"
	CatNameConflict        Category = "name_conflict"
	CatDanglingReference   Category = "dangling_reference"
	CatReferenceUnverified Category = "reference_unverified"
	CatTimeRange           Category = "time_range_mismatch"
	CatUnexpectedFile      Category = "unexpected_file"
	CatParseError          Category = "parse_error"
)

// Issue is a single verification finding, located to file and record.
type Issue struct {
	Category     Category `json:"category"`
	Version      string   `json:"version,omitempty"`
	File         string   `json:"file,omitempty"`
	Record       string   `json:"record,omitempty"`
	TargetFile   string   `json:"targetFile,omitempty"`
	TargetRecord string   `json:"targetRecord,omitempty"`
	Detail       string   `json:"detail"`
}

// Rejection is returned when an inbound chunk or commit is refused.
type Rejection struct {
	Issue Issue
}

func (r *Rejection) Error() string {
	return string(r.Issue.Category) + ": " + r.Issue.Detail
}

// FileResult summarizes one verified file.
type FileResult struct {
	Name    string    `json:"name"`
	Kind    Kind      `json:"kind"`
	Records int       `json:"records"`
	Empty   bool      `json:"empty"`
	Actual  TimeRange `json:"actualRange"`
}

// Report is the outcome of one commit attempt.
type Report struct {
	Version        string       `json:"version"`
	ManifestDigest string       `json:"manifestDigest"`
	Status         string       `json:"status"` // "sealed" or "rejected"
	Issues         []Issue      `json:"issues"`
	Files          []FileResult `json:"files"`
}

// HasCategory reports whether the report contains an issue of the category.
func (r *Report) HasCategory(c Category) bool {
	for _, iss := range r.Issues {
		if iss.Category == c {
			return true
		}
	}
	return false
}

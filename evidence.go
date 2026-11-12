package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Record kinds appearing in an evidence bundle, in protocol order.
const (
	KindSnapshot  = "snapshot"
	KindTest      = "test"
	KindInput     = "input"
	KindExecStart = "exec_start"
	KindExecEnd   = "exec_end"
	KindSeal      = "seal"
)

// Execution statuses recorded in an exec_end payload.
const (
	StatusPassed      = "passed"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
)

const zeroDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// Payload carries kind-specific data. Unused fields are omitted so each
// record's canonical form only contains what its kind requires.
type Payload struct {
	Name         string    `json:"name,omitempty"`
	Args         []string  `json:"args,omitempty"`
	SourceDigest string    `json:"source_digest,omitempty"`
	TestDigest   string    `json:"test_digest,omitempty"`
	InputDigest  string    `json:"input_digest,omitempty"`
	Start        time.Time `json:"start,omitempty"`
	End          time.Time `json:"end,omitempty"`
	ExitCode     *int      `json:"exit_code,omitempty"`
	Signal       string    `json:"signal,omitempty"`
	Status       string    `json:"status,omitempty"`
	Head         string    `json:"head,omitempty"`
}

// Record is one link in the ordered digest chain. Digest covers every field
// except itself, including Prev, so any edit, drop, or reorder breaks the
// chain at the first modified link.
type Record struct {
	Seq      int       `json:"seq"`
	Kind     string    `json:"kind"`
	Time     time.Time `json:"time"`
	Snapshot string    `json:"snapshot"`
	Payload  Payload   `json:"payload"`
	Prev     string    `json:"prev"`
	Digest   string    `json:"digest"`
}

func (r Record) computeDigest() string {
	r.Digest = ""
	data, err := json.Marshal(r)
	if err != nil {
		panic(fmt.Sprintf("evidence: marshal record: %v", err))
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Bundle is a sealed, self-contained evidence package.
type Bundle struct {
	Version  int      `json:"version"`
	Snapshot string   `json:"snapshot"`
	Records  []Record `json:"records"`
	Head     string   `json:"head"`
}

// Marshal serializes the bundle to canonical JSON.
func (b *Bundle) Marshal() ([]byte, error) {
	return json.Marshal(b)
}

// UnmarshalBundle parses a serialized bundle.
func UnmarshalBundle(data []byte) (*Bundle, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("evidence: unmarshal bundle: %w", err)
	}
	return &b, nil
}

// SourceDigest computes a deterministic digest of every regular file under
// root (relative path + content), excluding .git. It identifies the exact
// code snapshot the recorded tests ran against.
func SourceDigest(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("evidence: walk source: %w", err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			return "", fmt.Errorf("evidence: relativize %s: %w", f, err)
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("evidence: read %s: %w", f, err)
		}
		fh := sha256.Sum256(data)
		fmt.Fprintf(h, "%s\x00%s\x00", filepath.ToSlash(rel), hex.EncodeToString(fh[:]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func digestString(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Step ops.
const (
	OpNoop          = "noop"          // everything consistent
	OpRebuildIndex  = "rebuild_index" // segments verified, index missing/corrupt
	OpUnrecoverable = "unrecoverable" // segment data lost/corrupt: never fabricated
)

type Step struct {
	ID     string `json:"id"`
	Op     string `json:"op"`
	Detail string `json:"detail"`
}

// Plan is the executable output of Audit. Apply refuses to run it
// unless the store still matches Summary (or a matching journal shows
// the summary was already verified and steps are being resumed).
type Plan struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Dir        string    `json:"dir"`
	Summary    string    `json:"summary"`
	Repairable bool      `json:"repairable"`
	Steps      []Step    `json:"steps"`
}

func jsonUnmarshal(data []byte, v interface{}) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}
	return nil
}

// computeSummary hashes everything Apply must re-verify before acting:
// the manifest bytes, each segment's verification outcome and the
// index bytes (or its absence).
func computeSummary(manifestBytes []byte, manifestRead bool, segs []SegmentStatus, indexBytes []byte, indexPresent bool, inv string) string {
	h := sha256.New()
	fmt.Fprintf(h, "manifest:%v:%x\n", manifestRead, sha256.Sum256(manifestBytes))
	for _, s := range segs {
		fmt.Fprintf(h, "seg:%s:%v:%v:%v:%s\n", s.Name, s.Present, s.SizeOK, s.DigestOK, s.Detail)
	}
	if indexPresent {
		fmt.Fprintf(h, "index:present:%x\n", sha256.Sum256(indexBytes))
	} else {
		h.Write([]byte("index:absent\n"))
	}
	fmt.Fprintf(h, "inventory:\n%s\n", inv)
	return hex.EncodeToString(h.Sum(nil))
}

// buildPlan turns an audit report into executable steps.
//
// Repair policy:
//   - manifest unreadable        -> unrecoverable (expected digests unknown)
//   - any segment missing/corrupt -> unrecoverable (content is never invented)
//   - segments OK, index bad      -> rebuild_index (index is derived data)
//   - all consistent              -> noop
func buildPlan(rep *Report) *Plan {
	p := &Plan{
		CreatedAt:  time.Now().UTC(),
		Dir:        rep.Dir,
		Summary:    rep.Summary,
		Repairable: true,
	}
	add := func(op, detail string) {
		p.Steps = append(p.Steps, Step{ID: fmt.Sprintf("step-%03d", len(p.Steps)+1), Op: op, Detail: detail})
	}

	switch {
	case !rep.ManifestOK:
		p.Repairable = false
		add(OpUnrecoverable, "manifest missing or unparsable: expected segment digests are unknown, cannot verify or repair safely")
	default:
		var bad []string
		for _, s := range rep.Segments {
			if !s.OK() {
				bad = append(bad, s.Name+" ("+s.Detail+")")
			}
		}
		if len(bad) > 0 {
			p.Repairable = false
			sort.Strings(bad)
			add(OpUnrecoverable, "segment data failed verification; content must not be fabricated: "+strings.Join(bad, ", "))
		}
		if !rep.IndexConsistent {
			if p.Repairable {
				add(OpRebuildIndex, "rebuild "+IndexName+" from verified segments: "+strings.Join(rep.IndexProblems, "; "))
			} else {
				add(OpNoop, "index also inconsistent, but rebuild is blocked until segments are restored")
			}
		}
	}
	if len(p.Steps) == 0 {
		add(OpNoop, "manifest, segments and index are consistent")
	}

	h := sha256.New()
	h.Write([]byte(p.Summary))
	for _, s := range p.Steps {
		h.Write([]byte(s.ID + s.Op + s.Detail))
	}
	p.ID = hex.EncodeToString(h.Sum(nil))[:16]
	return p
}

package checksumsync

import (
	"fmt"
	"sort"
	"sync/atomic"
	"time"
)

// MismatchKind classifies a diverged primary key.
type MismatchKind int

const (
	// Modified: the PK exists on both sides but the payload differs (tampering).
	Modified MismatchKind = iota
	// MissingInTarget: the PK exists only in the source (lost change).
	MissingInTarget
	// ExtraInTarget: the PK exists only in the target (phantom row).
	ExtraInTarget
)

func (k MismatchKind) String() string {
	switch k {
	case Modified:
		return "MODIFIED"
	case MissingInTarget:
		return "MISSING_IN_TARGET"
	case ExtraInTarget:
		return "EXTRA_IN_TARGET"
	}
	return "UNKNOWN"
}

// Mismatch pinpoints one diverged primary key.
type Mismatch struct {
	Kind        MismatchKind
	Bucket      int
	PK          uint64
	SourceValue string
	TargetValue string
}

func (m Mismatch) String() string {
	return fmt.Sprintf("%s bucket=%d pk=%d source=%q target=%q",
		m.Kind, m.Bucket, m.PK, m.SourceValue, m.TargetValue)
}

// Report is the result of one inspection cycle.
type Report struct {
	StartedAt  time.Time
	Duration   time.Duration
	BadBuckets []int
	Mismatches []Mismatch
}

// Consistent reports whether the two tables matched exactly.
func (r *Report) Consistent() bool { return len(r.Mismatches) == 0 }

// Compare runs one full inspection cycle: it first compares the NumBuckets
// rolling digests of both tables (O(NumBuckets)), and drills down only into
// the buckets whose digest differs, locating the exact primary keys.
func Compare(source, target *Table) *Report {
	start := time.Now()
	rep := &Report{StartedAt: start}

	srcDigests := source.SnapshotDigests()
	tgtDigests := target.SnapshotDigests()

	for b := 0; b < NumBuckets; b++ {
		if srcDigests[b] != tgtDigests[b] {
			rep.BadBuckets = append(rep.BadBuckets, b)
		}
	}

	for _, b := range rep.BadBuckets {
		rep.Mismatches = append(rep.Mismatches, drillDown(source, target, b)...)
	}

	sort.Slice(rep.Mismatches, func(i, j int) bool {
		return rep.Mismatches[i].PK < rep.Mismatches[j].PK
	})
	rep.Duration = time.Since(start)
	return rep
}

// drillDown compares row-level hashes inside one diverged bucket and returns
// the exact primary keys that differ.
func drillDown(source, target *Table, b int) []Mismatch {
	srcRows := source.rowsInBucket(b)
	tgtRows := target.rowsInBucket(b)

	var out []Mismatch
	for pk, sv := range srcRows {
		tv, ok := tgtRows[pk]
		if !ok {
			out = append(out, Mismatch{Kind: MissingInTarget, Bucket: b, PK: pk, SourceValue: sv})
			continue
		}
		if hashRow(pk, sv) != hashRow(pk, tv) {
			out = append(out, Mismatch{Kind: Modified, Bucket: b, PK: pk, SourceValue: sv, TargetValue: tv})
		}
	}
	for pk, tv := range tgtRows {
		if _, ok := srcRows[pk]; !ok {
			out = append(out, Mismatch{Kind: ExtraInTarget, Bucket: b, PK: pk, TargetValue: tv})
		}
	}
	return out
}

// Inspector continuously patrols the two tables while changes keep flowing.
// Because live replication has a small lag, a divergence is only reported
// after it persists across two consecutive cycles (confirmation), which
// filters out in-flight changes while still detecting real loss/tampering
// within ~2 cycles (milliseconds).
type Inspector struct {
	Source *Table
	Target *Table
	// Interval between patrol cycles.
	Interval time.Duration
	// OnDetect is called (once per confirmed divergence set change) with the
	// confirmed report. Must be non-blocking or fast.
	OnDetect func(*Report)
	// cycles counts completed patrol cycles (for observability/tests).
	cycles int64

	stop chan struct{}
	done chan struct{}
}

func NewInspector(source, target *Table, interval time.Duration) *Inspector {
	return &Inspector{
		Source:   source,
		Target:   target,
		Interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Cycles returns the number of completed patrol cycles.
func (in *Inspector) Cycles() int64 {
	return atomic.LoadInt64(&in.cycles)
}

// Run starts the patrol loop and blocks until Stop is called.
func (in *Inspector) Run() {
	defer close(in.done)
	ticker := time.NewTicker(in.Interval)
	defer ticker.Stop()

	pending := make(map[uint64]Mismatch) // candidates seen in the previous cycle
	confirmed := make(map[uint64]Mismatch)

	for {
		select {
		case <-in.stop:
			return
		case <-ticker.C:
		}

		rep := Compare(in.Source, in.Target)
		atomic.AddInt64(&in.cycles, 1)

		current := make(map[uint64]Mismatch, len(rep.Mismatches))
		for _, m := range rep.Mismatches {
			current[m.PK] = m
		}

		// A divergence is confirmed when it shows up in two consecutive
		// cycles, i.e. it is not just replication lag.
		fresh := false
		for pk, m := range current {
			if _, wasPending := pending[pk]; !wasPending {
				continue
			}
			if _, already := confirmed[pk]; already {
				continue
			}
			confirmed[pk] = m
			fresh = true
		}
		// Forget confirmed divergences that have been repaired.
		for pk := range confirmed {
			if _, stillBad := current[pk]; !stillBad {
				delete(confirmed, pk)
			}
		}
		pending = current

		if fresh && in.OnDetect != nil {
			cp := *rep
			cp.Mismatches = make([]Mismatch, 0, len(confirmed))
			for _, m := range confirmed {
				cp.Mismatches = append(cp.Mismatches, m)
			}
			in.OnDetect(&cp)
		}
	}
}

// Stop halts the patrol loop and waits for it to exit.
func (in *Inspector) Stop() {
	close(in.stop)
	<-in.done
}

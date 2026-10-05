package rename

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Journal is a write-ahead log of plan execution. Each operation is
// recorded as "BEGIN i src dst" before it runs and "DONE i" after it
// completes, so a crash at any point leaves enough state to resume.
type Journal struct {
	w io.Writer
}

// NewJournal returns a Journal appending to w. The caller is responsible
// for flushing/syncing w if it is backed by a file.
func NewJournal(w io.Writer) *Journal { return &Journal{w: w} }

func (j *Journal) begin(i int, op Op) {
	fmt.Fprintf(j.w, "BEGIN %d %s %s\n", i, op.Src, op.Dst)
}

func (j *Journal) done(i int) {
	fmt.Fprintf(j.w, "DONE %d\n", i)
}

// RenameFunc performs one atomic rename of base-relative paths.
type RenameFunc func(src, dst string) error

// Execute runs ops in order, journaling every step. On failure it
// returns after the failed op's BEGIN has been logged, so Recover can
// later determine how far execution actually got.
func Execute(ops []Op, rename RenameFunc, j *Journal) error {
	for i, op := range ops {
		j.begin(i, op)
		if err := rename(op.Src, op.Dst); err != nil {
			return fmt.Errorf("execute: op %d (%s -> %s): %w", i, op.Src, op.Dst, err)
		}
		j.done(i)
	}
	return nil
}

// Recover inspects the journal of an interrupted Execute and returns the
// operations that still need to run to finish the plan. exists reports
// whether a base-relative path currently exists on disk.
//
// The last BEGIN without a matching DONE is ambiguous on its own — the
// crash may have happened before or after the actual rename — so Recover
// consults the filesystem: if the source is still there the op is
// redone; if only the destination is there the op already happened and
// is skipped. Any other combination is corruption and an error.
func Recover(journal io.Reader, ops []Op, exists func(rel string) bool) ([]Op, error) {
	pending := map[int]bool{}
	last := -1
	sc := bufio.NewScanner(journal)
	for sc.Scan() {
		line := sc.Text()
		f := strings.Fields(line)
		if len(f) < 2 {
			return nil, fmt.Errorf("recover: malformed journal line %q", line)
		}
		i, err := strconv.Atoi(f[1])
		if err != nil || i < 0 || i >= len(ops) {
			return nil, fmt.Errorf("recover: bad op index in journal line %q", line)
		}
		switch f[0] {
		case "BEGIN":
			if len(f) != 4 || f[2] != ops[i].Src || f[3] != ops[i].Dst {
				return nil, fmt.Errorf("recover: journal op %d does not match the plan", i)
			}
			pending[i] = true
			if i > last {
				last = i
			}
		case "DONE":
			delete(pending, i)
		default:
			return nil, fmt.Errorf("recover: unknown journal record %q", f[0])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	resume := last + 1
	if len(pending) > 0 {
		if len(pending) > 1 || !pending[last] {
			return nil, fmt.Errorf("recover: journal has multiple incomplete ops")
		}
		op := ops[last]
		srcThere := exists(op.Src)
		dstThere := exists(op.Dst)
		switch {
		case srcThere && !dstThere:
			resume = last // rename never happened; redo the op
		case !srcThere && dstThere:
			resume = last + 1 // rename happened but DONE was lost
		default:
			return nil, fmt.Errorf("recover: ambiguous state for op %d (src exists=%v, dst exists=%v)",
				last, srcThere, dstThere)
		}
	}
	return ops[resume:], nil
}

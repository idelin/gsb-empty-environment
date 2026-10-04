package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// LogEntry is one transaction in the operation log (JSON Lines format).
type LogEntry struct {
	Seq  int    `json:"seq"`
	Op   string `json:"op"`             // create | rename | unlink
	Path string `json:"path,omitempty"` // create, unlink
	Src  string `json:"src,omitempty"`  // rename
	Dst  string `json:"dst,omitempty"`  // rename
	Type string `json:"type,omitempty"` // create: "dir" | "file" (default file)
}

// ParseLog reads JSON Lines, skipping blank lines and '#' comments.
func ParseLog(r io.Reader) ([]LogEntry, error) {
	var entries []LogEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		var e LogEntry
		if err := json.Unmarshal([]byte(text), &e); err != nil {
			return nil, fmt.Errorf("line %d: invalid log entry: %v", line, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// Step is the outcome of replaying one log entry.
type Step struct {
	Entry   LogEntry
	Events  []string
	Err     error
	Mapping map[string]NodeID
}

// Report is the full replay result.
type Report struct {
	Steps       []Step
	Issues      []string
	Recreations []string
	Diverged    bool
	Failed      int
}

func equalMapping(a, b map[string]NodeID) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// Replay executes every entry as an atomic transaction on the primary
// Tree and the independent Model, cross-checking their path -> identity
// mappings after each step and auditing the log via Checker.
func Replay(entries []LogEntry) *Report {
	tree := NewTree()
	model := NewModel()
	checker := NewChecker()
	rep := &Report{}

	for _, e := range entries {
		checker.CheckSeq(e.Seq)
		step := Step{Entry: e}

		var err error
		switch e.Op {
		case "create":
			dir := e.Type == "dir"
			n, terr := tree.Create(e.Path, dir)
			if terr != nil {
				err = terr
				break
			}
			mid, merr := model.Create(e.Path, dir)
			if merr != nil || NodeID(mid) != n.ID {
				err = fmt.Errorf("model divergence on create: model id=%d err=%v, tree id=%d", mid, merr, n.ID)
				rep.Diverged = true
				break
			}
			checker.NoteCreate(e.Path, n.ID)
			step.Events = append(step.Events, fmt.Sprintf("created %s id=%d", kindOf(dir), n.ID))
		case "unlink":
			n, terr := tree.Unlink(e.Path)
			if terr != nil {
				err = terr
				break
			}
			mid, merr := model.Unlink(e.Path)
			if merr != nil || NodeID(mid) != n.ID {
				err = fmt.Errorf("model divergence on unlink: model id=%d err=%v, tree id=%d", mid, merr, n.ID)
				rep.Diverged = true
				break
			}
			checker.NoteUnlink(e.Path, n.ID)
			step.Events = append(step.Events, fmt.Sprintf("unlinked id=%d", n.ID))
		case "rename":
			moved, replaced, terr := tree.Rename(e.Src, e.Dst)
			if terr != nil {
				err = terr
				break
			}
			mMoved, mReplaced, mHasRep, merr := model.Rename(e.Src, e.Dst)
			if merr != nil || NodeID(mMoved) != moved.ID ||
				(replaced != nil) != mHasRep ||
				(replaced != nil && NodeID(mReplaced) != replaced.ID) {
				err = fmt.Errorf("model divergence on rename: model moved=%d replaced=%d(%v) err=%v",
					mMoved, mReplaced, mHasRep, merr)
				rep.Diverged = true
				break
			}
			if replaced != nil {
				checker.NoteUnlink(e.Dst, replaced.ID)
				step.Events = append(step.Events,
					fmt.Sprintf("replaced target id=%d at %s", replaced.ID, e.Dst))
			}
			step.Events = append(step.Events,
				fmt.Sprintf("moved id=%d: %s -> %s", moved.ID, e.Src, e.Dst))
		default:
			err = fmt.Errorf("unknown op %q", e.Op)
		}

		step.Err = err
		tm, mm := tree.Mapping(), model.Mapping()
		if !equalMapping(tm, mm) {
			rep.Diverged = true
			step.Events = append(step.Events, "FATAL: tree/model mapping divergence")
		}
		step.Mapping = tm
		if err != nil {
			rep.Failed++
		}
		rep.Steps = append(rep.Steps, step)
		if rep.Diverged {
			break // state can no longer be trusted
		}
	}

	rep.Issues = checker.Issues
	rep.Recreations = checker.Recreations
	return rep
}

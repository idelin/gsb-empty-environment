package netsec

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// RunSpec describes one test execution to record.
type RunSpec struct {
	TestName string   // logical test/assertion name
	Input    []byte   // fed to the process on stdin and hashed as input digest
	Path     string   // executable
	Args     []string // arguments
	Env      []string // if non-nil, replaces the process environment
	Dir      string   // working directory; empty means current directory
}

// RunResult reports how a recorded execution ended.
type RunResult struct {
	Status   string
	ExitCode int // -1 when no exit code was produced (signal, start failure)
	Signal   string
	Start    time.Time
	End      time.Time
	Err      error
}

// Recorder is used by the Go test host to build an evidence bundle. It is
// not safe for concurrent use; record runs sequentially.
type Recorder struct {
	snapshot string
	records  []Record
	head     string
	sealed   bool
}

// NewRecorder snapshots the source tree at sourceRoot and opens a new
// bundle with the snapshot record as chain anchor.
func NewRecorder(sourceRoot string) (*Recorder, error) {
	sd, err := SourceDigest(sourceRoot)
	if err != nil {
		return nil, err
	}
	r := &Recorder{snapshot: sd, head: zeroDigest}
	r.append(KindSnapshot, Payload{SourceDigest: sd})
	return r, nil
}

func (r *Recorder) append(kind string, p Payload) {
	rec := Record{
		Seq:      len(r.records),
		Kind:     kind,
		Time:     time.Now().UTC(),
		Snapshot: r.snapshot,
		Payload:  p,
		Prev:     r.head,
	}
	rec.Digest = rec.computeDigest()
	r.records = append(r.records, rec)
	r.head = rec.Digest
}

// RecordRun appends test, input, and exec_start records, runs the process
// to completion (or interruption), then appends the exec_end record. The
// exec_end record is always written, so a started execution is never left
// open, and its status is derived solely from the real process outcome.
func (r *Recorder) RecordRun(ctx context.Context, spec RunSpec) (RunResult, error) {
	if r.sealed {
		return RunResult{}, errors.New("evidence: recorder already sealed")
	}
	testDigest := digestString("test", spec.TestName)
	inputDigest := digestBytes(spec.Input)

	r.append(KindTest, Payload{Name: spec.TestName, TestDigest: testDigest})
	r.append(KindInput, Payload{InputDigest: inputDigest})

	start := time.Now().UTC()
	r.append(KindExecStart, Payload{
		Name:        spec.Path,
		Args:        spec.Args,
		Start:       start,
		TestDigest:  testDigest,
		InputDigest: inputDigest,
	})

	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Stdin = bytes.NewReader(spec.Input)
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	cmd.Dir = spec.Dir
	runErr := cmd.Run()
	end := time.Now().UTC()

	res := runResultFrom(start, end, runErr, ctx)
	p := Payload{
		Name:        spec.Path,
		Start:       start,
		End:         end,
		Signal:      res.Signal,
		Status:      res.Status,
		TestDigest:  testDigest,
		InputDigest: inputDigest,
	}
	if res.ExitCode >= 0 {
		code := res.ExitCode
		p.ExitCode = &code
	}
	r.append(KindExecEnd, p)
	return res, nil
}

func runResultFrom(start, end time.Time, runErr error, ctx context.Context) RunResult {
	res := RunResult{Start: start, End: end, Err: runErr, ExitCode: -1}
	if runErr == nil {
		res.Status = StatusPassed
		res.ExitCode = 0
		return res
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			res.ExitCode = code
		} else if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.Signal = ws.Signal().String()
		}
	}
	switch {
	case ctx.Err() != nil || res.Signal != "":
		res.Status = StatusInterrupted
	default:
		res.Status = StatusFailed
	}
	return res
}

// Seal appends the final seal record and returns the completed bundle.
func (r *Recorder) Seal() *Bundle {
	if !r.sealed {
		r.append(KindSeal, Payload{Head: r.head})
		r.sealed = true
	}
	return &Bundle{
		Version:  1,
		Snapshot: r.snapshot,
		Records:  append([]Record(nil), r.records...),
		Head:     r.head,
	}
}

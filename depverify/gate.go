package depverify

import (
	"fmt"
	"sort"
)

// GateError marks a build-gate failure and wraps the underlying cause.
type GateError struct {
	Path    string
	Version string
	Reason  string
	Err     error
}

func (e *GateError) Error() string {
	base := e.Path
	if e.Version != "" {
		base = e.Path + "@" + e.Version
	}
	if e.Err != nil {
		return base + ": " + e.Reason + ": " + e.Err.Error()
	}
	return base + ": " + e.Reason
}

func (e *GateError) Unwrap() error { return e.Err }

// BuildGate is handed to the build pipeline after verification. Every module
// the build consumes must be checked out through the gate, which confirms the
// content digest matches the verified binding. Content that was never verified
// (or changed after verification) cannot reach the compiler.
type BuildGate struct {
	report   *Report
	used     map[string]bool
	closed   bool
	failures []error
}

// NewBuildGate wraps a successful verification Report.
func NewBuildGate(report *Report) *BuildGate {
	return &BuildGate{report: report, used: map[string]bool{}}
}

// Use registers a module the build is about to compile and verifies that the
// digest presented at checkout time equals the verified content digest.
func (g *BuildGate) Use(path, version, digest string) error {
	if g.closed {
		return &GateError{Path: path, Version: version, Reason: "gate already closed"}
	}
	vm, ok := g.report.Modules[path]
	if !ok {
		err := &GateError{Path: path, Version: version, Reason: "module was not verified"}
		g.failures = append(g.failures, err)
		return err
	}
	expected := vm.Version
	if vm.Replaced {
		expected = vm.EffectiveVersion
	}
	if expected != version {
		err := &GateError{Path: path, Version: version,
			Reason: fmt.Sprintf("version mismatch (verified %s, effective %s)",
				vm.Version, vm.EffectiveVersion)}
		g.failures = append(g.failures, err)
		return err
	}
	if vm.Digest != digest {
		err := &GateError{Path: path, Version: version,
			Reason: fmt.Sprintf("content digest %s does not match verified %s", digest, vm.Digest),
			Err:    ErrChecksumMismatch}
		g.failures = append(g.failures, err)
		return err
	}
	g.used[path] = true
	return nil
}

// Complete closes the gate and fails if any verified module was never used or
// any Use call was rejected. The build service must call this before invoking
// the compiler so partial/tampered module sets cannot slip through.
func (g *BuildGate) Complete() error {
	g.closed = true
	var errs []error
	errs = append(errs, g.failures...)
	var unused []string
	for path, vm := range g.report.Modules {
		if !g.used[path] {
			unused = append(unused, path+"@"+vm.Version)
		}
	}
	sort.Strings(unused)
	for _, m := range unused {
		errs = append(errs, &GateError{Reason: "verified module not consumed by build: " + m})
	}
	if len(errs) > 0 {
		return &MultiError{Errors: errs}
	}
	return nil
}

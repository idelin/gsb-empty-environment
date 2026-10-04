// Package casestore persists a minimal replayable sample (the raw input)
// together with everything needed to interpret and reproduce the run:
// outcomes, verdict, normalization rules, executor commands, and an
// environment summary.
package casestore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"difftest/internal/judge"
	"difftest/internal/normalize"
	"difftest/internal/runner"
)

const (
	InputFile = "input.bin"
	MetaFile  = "case.json"
)

// EnvSummary captures the environment the run happened in.
type EnvSummary struct {
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	GoVersion string    `json:"go_version"`
	Hostname  string    `json:"hostname"`
	TimeUTC   time.Time `json:"time_utc"`
}

// Case is one replayable differential test case.
type Case struct {
	ExecA     string          `json:"exec_a"`
	ExecB     string          `json:"exec_b"`
	Rules     normalize.Rules `json:"rules"`
	OutcomeA  runner.Outcome  `json:"outcome_a"`
	OutcomeB  runner.Outcome  `json:"outcome_b"`
	Verdict   judge.Result    `json:"verdict"`
	Env       EnvSummary      `json:"env"`
	InputSHA  string          `json:"input_sha256"`
	InputSize int             `json:"input_size"`
}

// CollectEnv gathers the environment summary.
func CollectEnv() EnvSummary {
	host, _ := os.Hostname()
	return EnvSummary{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		Hostname:  host,
		TimeUTC:   time.Now().UTC(),
	}
}

// New builds a Case from a finished run.
func New(execA, execB string, rules normalize.Rules, input []byte,
	a, b runner.Outcome, v judge.Result) *Case {
	sum := sha256.Sum256(input)
	return &Case{
		ExecA:     execA,
		ExecB:     execB,
		Rules:     rules,
		OutcomeA:  a,
		OutcomeB:  b,
		Verdict:   v,
		Env:       CollectEnv(),
		InputSHA:  hex.EncodeToString(sum[:]),
		InputSize: len(input),
	}
}

// Save writes input.bin and case.json into dir.
func Save(dir string, c *Case, input []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, InputFile), input, 0o644); err != nil {
		return fmt.Errorf("write input: %w", err)
	}
	meta, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, MetaFile), meta, 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	return nil
}

// Load reads a saved case directory.
func Load(dir string) (*Case, []byte, error) {
	meta, err := os.ReadFile(filepath.Join(dir, MetaFile))
	if err != nil {
		return nil, nil, fmt.Errorf("read metadata: %w", err)
	}
	var c Case
	if err := json.Unmarshal(meta, &c); err != nil {
		return nil, nil, fmt.Errorf("parse metadata: %w", err)
	}
	input, err := os.ReadFile(filepath.Join(dir, InputFile))
	if err != nil {
		return nil, nil, fmt.Errorf("read input: %w", err)
	}
	sum := sha256.Sum256(input)
	if got := hex.EncodeToString(sum[:]); got != c.InputSHA {
		return nil, nil, fmt.Errorf("input checksum mismatch: saved %s, got %s", c.InputSHA, got)
	}
	return &c, input, nil
}

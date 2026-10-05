package difftest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ToolVersion is stamped into every saved case.
const ToolVersion = "0.1.0"

// EnvSummary captures enough of the environment to make a saved case
// reproducible and diagnosable elsewhere.
type EnvSummary struct {
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	Kernel    string    `json:"kernel,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	GoVersion string    `json:"go_version"`
	NumCPU    int       `json:"num_cpu"`
	WorkDir   string    `json:"work_dir"`
	Timestamp time.Time `json:"timestamp"`
}

// CaseFile is the serialized record of one differential run.
type CaseFile struct {
	Version   int         `json:"version"`
	Tool      string      `json:"tool"`
	Env       EnvSummary  `json:"env"`
	Executors [2]Executor `json:"executors"`
	Rules     Rules       `json:"rules"`
	InputFile string      `json:"input_file"`
	InputSHA  string      `json:"input_sha256"`
	Outcomes  [2]Outcome  `json:"outcomes"`
	Verdict   Verdict     `json:"verdict"`
	Minimized bool        `json:"minimized"`
}

// CollectEnv gathers the environment summary best-effort.
func CollectEnv() EnvSummary {
	host, _ := os.Hostname()
	wd, _ := os.Getwd()
	kernel := ""
	if out, err := exec.Command("uname", "-sr").Output(); err == nil {
		kernel = strings.TrimSpace(string(out))
	}
	return EnvSummary{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Kernel:    kernel,
		Hostname:  host,
		GoVersion: runtime.Version(),
		NumCPU:    runtime.NumCPU(),
		WorkDir:   wd,
		Timestamp: time.Now().UTC(),
	}
}

// SaveCase writes input.txt and case.json into dir, creating it if needed.
func SaveCase(dir string, input []byte, execA, execB Executor, rules Rules, outcomes [2]Outcome, verdict Verdict, minimized bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0o644); err != nil {
		return err
	}
	sum := sha256.Sum256(input)
	cf := CaseFile{
		Version:   1,
		Tool:      "difftest/" + ToolVersion,
		Env:       CollectEnv(),
		Executors: [2]Executor{execA, execB},
		Rules:     rules,
		InputFile: "input.txt",
		InputSHA:  hex.EncodeToString(sum[:]),
		Outcomes:  outcomes,
		Verdict:   verdict,
		Minimized: minimized,
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "case.json"), data, 0o644)
}

// LoadCase reads a saved case directory and returns the case plus input.
func LoadCase(dir string) (CaseFile, []byte, error) {
	var cf CaseFile
	data, err := os.ReadFile(filepath.Join(dir, "case.json"))
	if err != nil {
		return cf, nil, err
	}
	if err := json.Unmarshal(data, &cf); err != nil {
		return cf, nil, fmt.Errorf("parse case.json: %w", err)
	}
	input, err := os.ReadFile(filepath.Join(dir, cf.InputFile))
	if err != nil {
		return cf, nil, err
	}
	sum := sha256.Sum256(input)
	if got := hex.EncodeToString(sum[:]); got != cf.InputSHA {
		return cf, nil, fmt.Errorf("input checksum mismatch: saved %s, got %s", cf.InputSHA, got)
	}
	return cf, input, nil
}

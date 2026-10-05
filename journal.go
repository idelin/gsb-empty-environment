package rename

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

// Journal records plan progress on disk so an interrupted run can resume
// exactly where it stopped. The file first lists every planned step, then
// one "D <i>" line per completed step, appended and fsynced as steps finish.
type Journal struct {
	path  string
	steps []Step
	done  map[int]bool
}

// OpenJournal opens the journal at path for plan, creating it if needed.
// An existing journal must describe the same plan, otherwise it is stale
// and an error is returned.
func OpenJournal(path string, plan *Plan) (*Journal, error) {
	j := &Journal{path: path, steps: plan.Steps, done: make(map[int]bool)}
	data, err := readFile(path)
	if os.IsNotExist(err) {
		if werr := writeFile(path, encodeSteps(plan.Steps)); werr != nil {
			return nil, werr
		}
		return j, nil
	}
	if err != nil {
		return nil, err
	}
	recorded, done, err := decodeJournal(data)
	if err != nil {
		return nil, fmt.Errorf("rename: journal %s: %v", path, err)
	}
	if !stepsEqual(recorded, plan.Steps) {
		return nil, fmt.Errorf("rename: journal %s does not match the plan", path)
	}
	j.done = done
	return j, nil
}

// Done reports whether step i is recorded as completed.
func (j *Journal) Done(i int) bool { return j.done[i] }

// MarkDone records step i as completed, appending and fsyncing the journal.
func (j *Journal) MarkDone(i int) error {
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "D\t%d\n", i); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	j.done[i] = true
	return nil
}

// FS is the filesystem the executor renames on.
type FS interface {
	Rename(from, to string) error
}

// Execute runs plan against fsys, recording progress in j. Steps already
// marked done in j are skipped, so re-running after a failure resumes the
// plan. If fsys also implements Exists(string) bool, a step whose source is
// gone and whose target already exists (crash between rename and journal
// write) is treated as done instead of failing.
func Execute(plan *Plan, fsys FS, j *Journal) error {
	type existFS interface {
		Exists(string) bool
	}
	e, canCheck := fsys.(existFS)
	for i, s := range plan.Steps {
		if j.Done(i) {
			continue
		}
		if canCheck && !e.Exists(s.From) && e.Exists(s.To) {
			if err := j.MarkDone(i); err != nil {
				return err
			}
			continue
		}
		if err := fsys.Rename(s.From, s.To); err != nil {
			return fmt.Errorf("rename: step %d (%s -> %s): %v", i, s.From, s.To, err)
		}
		if err := j.MarkDone(i); err != nil {
			return err
		}
	}
	return nil
}

func encodeSteps(steps []Step) string {
	var b strings.Builder
	for i, s := range steps {
		fmt.Fprintf(&b, "S\t%d\t%s\t%s\t%t\n", i, s.From, s.To, s.Temp)
	}
	return b.String()
}

func decodeJournal(data string) ([]Step, map[int]bool, error) {
	var steps []Step
	done := make(map[int]bool)
	for n, line := range strings.Split(strings.TrimRight(data, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		switch f[0] {
		case "S":
			if len(f) != 5 {
				return nil, nil, fmt.Errorf("line %d: malformed step record", n+1)
			}
			idx, err := strconv.Atoi(f[1])
			if err != nil || idx != len(steps) {
				return nil, nil, fmt.Errorf("line %d: bad step index %q", n+1, f[1])
			}
			steps = append(steps, Step{From: f[2], To: f[3], Temp: f[4] == "true"})
		case "D":
			if len(f) != 2 {
				return nil, nil, fmt.Errorf("line %d: malformed done record", n+1)
			}
			idx, err := strconv.Atoi(f[1])
			if err != nil || idx < 0 || idx >= len(steps) {
				return nil, nil, fmt.Errorf("line %d: bad done index %q", n+1, f[1])
			}
			done[idx] = true
		default:
			return nil, nil, fmt.Errorf("line %d: unknown record %q", n+1, f[0])
		}
	}
	return steps, done, nil
}

func stepsEqual(a, b []Step) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readFile(path string) (string, error) {
	data, err := ioutil.ReadFile(path)
	return string(data), err
}

func writeFile(path, data string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

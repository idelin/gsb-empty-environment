package schedule

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// request is one JSON command line.
//
// Times use RFC3339 with an explicit offset, e.g. "2026-01-05T09:30:00+08:00"
// ("Z" is accepted too). Dates use "2006-01-02".
type request struct {
	Op          string `json:"op"`
	Rule        string `json:"rule"`
	ID          string `json:"id"`
	Start       string `json:"start"` // new_rule: Monday date
	Weeks       int    `json:"weeks"`
	Week        *int   `json:"week"` // override/cancel/restore: week index
	Date        string `json:"date"` // ... or a civil date inside the week
	Rev         int    `json:"rev"`
	FromWeek    int    `json:"from_week"`
	Weekday     int    `json:"weekday"`
	Minute      int    `json:"minute"`
	DurationMin int    `json:"duration_min"`
	OffsetMin   int    `json:"offset_min"`
	From        string `json:"from"` // query: RFC3339
	To          string `json:"to"`   // query: RFC3339
	FromVersion int    `json:"from_version"`
	ToVersion   int    `json:"to_version"`
}

type instanceJSON struct {
	ID    string `json:"id"`
	Week  int    `json:"week"`
	Rev   int    `json:"rev"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type changeJSON struct {
	Kind string        `json:"kind"`
	ID   string        `json:"id"`
	Week int           `json:"week"`
	Old  *instanceJSON `json:"old,omitempty"`
	New  *instanceJSON `json:"new,omitempty"`
}

type response struct {
	OK        bool           `json:"ok"`
	Version   *int           `json:"version,omitempty"`
	Instances []instanceJSON `json:"instances,omitempty"`
	Changes   []changeJSON   `json:"changes,omitempty"`
	Error     string         `json:"error,omitempty"`
}

func fail(err error) response { return response{OK: false, Error: err.Error()} }

// formatInZone renders t with an explicit numeric offset, e.g. +08:00.
func formatInZone(t time.Time, offsetMin int) string {
	sec, err := minutesToSeconds(offsetMin)
	if err != nil {
		sec = 0
	}
	return t.In(time.FixedZone("", int(sec))).Format(time.RFC3339)
}

func toJSON(inst Instance) instanceJSON {
	return instanceJSON{
		ID:    inst.ID,
		Week:  inst.Week,
		Rev:   inst.Rev,
		Start: formatInZone(inst.Start, inst.OffsetMin),
		End:   formatInZone(inst.End, inst.OffsetMin),
	}
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q: want RFC3339 with explicit offset", s)
	}
	return t.UTC(), nil
}

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: want YYYY-MM-DD", s)
	}
	return t, nil
}

func (req *request) params() Params {
	return Params{
		Rev:         req.Rev,
		FromWeek:    req.FromWeek,
		Weekday:     req.Weekday,
		MinuteOfDay: req.Minute,
		DurationMin: req.DurationMin,
		OffsetMin:   req.OffsetMin,
	}
}

// resolveWeek accepts either an explicit week index or a civil date,
// which is converted via the rule's original start Monday.
func resolveWeek(s *Scheduler, req *request) (int, error) {
	if req.Week != nil {
		return *req.Week, nil
	}
	if req.Date != "" {
		d, err := parseDate(req.Date)
		if err != nil {
			return 0, err
		}
		return s.WeekOfDate(req.Rule, d)
	}
	return 0, errors.New("week or date required")
}

func handleLine(s *Scheduler, line []byte) response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return fail(fmt.Errorf("invalid JSON: %w", err))
	}
	switch req.Op {
	case "new_rule":
		start, err := parseDate(req.Start)
		if err != nil {
			return fail(err)
		}
		if err := s.NewRule(req.ID, start, req.Weeks); err != nil {
			return fail(err)
		}
		v := 0
		return response{OK: true, Version: &v}
	case "add_revision":
		v, err := s.AddRevision(req.Rule, req.params())
		if err != nil {
			return fail(err)
		}
		return response{OK: true, Version: &v}
	case "override":
		week, err := resolveWeek(s, &req)
		if err != nil {
			return fail(err)
		}
		v, err := s.SetOverride(req.Rule, week, req.params())
		if err != nil {
			return fail(err)
		}
		return response{OK: true, Version: &v}
	case "cancel", "restore":
		week, err := resolveWeek(s, &req)
		if err != nil {
			return fail(err)
		}
		var v int
		if req.Op == "cancel" {
			v, err = s.Cancel(req.Rule, week)
		} else {
			v, err = s.Restore(req.Rule, week)
		}
		if err != nil {
			return fail(err)
		}
		return response{OK: true, Version: &v}
	case "query":
		qs, err := parseTime(req.From)
		if err != nil {
			return fail(err)
		}
		qe, err := parseTime(req.To)
		if err != nil {
			return fail(err)
		}
		insts, err := s.Query(req.Rule, qs, qe)
		if err != nil {
			return fail(err)
		}
		out := make([]instanceJSON, 0, len(insts))
		for _, inst := range insts {
			out = append(out, toJSON(inst))
		}
		return response{OK: true, Instances: out}
	case "diff":
		changes, err := s.Diff(req.Rule, req.FromVersion, req.ToVersion)
		if err != nil {
			return fail(err)
		}
		out := make([]changeJSON, 0, len(changes))
		for _, c := range changes {
			cj := changeJSON{Kind: string(c.Kind), ID: c.ID, Week: c.Week}
			if c.Old != nil {
				o := toJSON(*c.Old)
				cj.Old = &o
			}
			if c.New != nil {
				n := toJSON(*c.New)
				cj.New = &n
			}
			out = append(out, cj)
		}
		return response{OK: true, Changes: out}
	default:
		return fail(fmt.Errorf("unknown op %q", req.Op))
	}
}

// RunCLI reads newline-delimited JSON commands from in and writes one
// JSON response per line to out.
func RunCLI(in io.Reader, out io.Writer) error {
	s := NewScheduler()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := enc.Encode(handleLine(s, line)); err != nil {
			return err
		}
	}
	return sc.Err()
}

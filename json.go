package recur

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TimeFormat is the JSON time format: RFC 3339 with an explicit numeric
// offset (or Z for UTC). Naive local times are rejected.
const TimeFormat = time.RFC3339

type ruleJSON struct {
	RuleID     string          `json:"rule_id"`
	StartDate  string          `json:"start_date"`
	Weeks      int             `json:"weeks"`
	Revisions  []revisionJSON  `json:"revisions"`
	Exceptions []exceptionJSON `json:"exceptions"`
}

type revisionJSON struct {
	FromSeq     int    `json:"from_seq"`
	Rev         int    `json:"rev"`
	Weekday     string `json:"weekday"`
	Minute      int    `json:"minute"`
	DurationMin int    `json:"duration_min"`
	OffsetMin   int    `json:"offset_min"`
}

type exceptionJSON struct {
	Seq         int     `json:"seq"`
	Rev         int     `json:"rev"`
	Action      string  `json:"action"`
	Weekday     *string `json:"weekday,omitempty"`
	Minute      *int    `json:"minute,omitempty"`
	DurationMin *int    `json:"duration_min,omitempty"`
	OffsetMin   *int    `json:"offset_min,omitempty"`
}

var weekdays = map[string]time.Weekday{
	"monday": time.Monday, "mon": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday,
	"friday": time.Friday, "fri": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday,
	"sunday": time.Sunday, "sun": time.Sunday,
}

// ParseWeekday accepts full or three-letter English weekday names.
func ParseWeekday(s string) (time.Weekday, error) {
	if wd, ok := weekdays[strings.ToLower(strings.TrimSpace(s))]; ok {
		return wd, nil
	}
	return 0, fmt.Errorf("recur: unknown weekday %q", s)
}

// ParseRule decodes and validates a JSON rule document.
func ParseRule(data []byte) (*Rule, error) {
	var rj ruleJSON
	if err := json.Unmarshal(data, &rj); err != nil {
		return nil, fmt.Errorf("recur: invalid JSON: %w", err)
	}
	start, err := time.Parse("2006-01-02", rj.StartDate)
	if err != nil {
		return nil, fmt.Errorf("recur: invalid start_date %q, want YYYY-MM-DD", rj.StartDate)
	}
	rule, err := NewRule(rj.RuleID, start, rj.Weeks)
	if err != nil {
		return nil, err
	}
	for i, rv := range rj.Revisions {
		wd, err := ParseWeekday(rv.Weekday)
		if err != nil {
			return nil, fmt.Errorf("revisions[%d]: %w", i, err)
		}
		rev := Revision{
			FromSeq: rv.FromSeq,
			Rev:     rv.Rev,
			Params: Params{
				Weekday:     wd,
				Minute:      rv.Minute,
				DurationMin: rv.DurationMin,
				OffsetMin:   rv.OffsetMin,
			},
		}
		if err := rule.UpsertRevision(rev); err != nil {
			return nil, fmt.Errorf("revisions[%d]: %w", i, err)
		}
	}
	if _, ok := rule.revisions[0]; !ok {
		return nil, fmt.Errorf("recur: a base revision with from_seq 0 is required")
	}
	for i, ex := range rj.Exceptions {
		exc := Exception{Seq: ex.Seq, Rev: ex.Rev, Action: Action(ex.Action)}
		if exc.Action == ActionOverride {
			if ex.Weekday == nil || ex.Minute == nil || ex.DurationMin == nil || ex.OffsetMin == nil {
				return nil, fmt.Errorf("exceptions[%d]: override requires weekday, minute, duration_min, offset_min", i)
			}
			wd, err := ParseWeekday(*ex.Weekday)
			if err != nil {
				return nil, fmt.Errorf("exceptions[%d]: %w", i, err)
			}
			exc.Params = Params{
				Weekday:     wd,
				Minute:      *ex.Minute,
				DurationMin: *ex.DurationMin,
				OffsetMin:   *ex.OffsetMin,
			}
		}
		if err := rule.UpsertException(exc); err != nil {
			return nil, fmt.Errorf("exceptions[%d]: %w", i, err)
		}
	}
	return rule, nil
}

// ParseTime parses an RFC 3339 timestamp with an explicit offset.
func ParseTime(s string) (int64, error) {
	t, err := time.Parse(TimeFormat, s)
	if err != nil {
		return 0, fmt.Errorf("recur: invalid time %q, want RFC 3339 with offset, e.g. 2026-01-05T09:00:00+08:00", s)
	}
	return t.Unix(), nil
}

// FormatTime renders UTC unix seconds in TimeFormat.
func FormatTime(unix int64) string {
	return time.Unix(unix, 0).UTC().Format(TimeFormat)
}

// InstanceJSON is the JSON view of an Instance.
type InstanceJSON struct {
	ID        string `json:"id"`
	Seq       int    `json:"seq"`
	Start     string `json:"start"`
	End       string `json:"end"`
	StartUnix int64  `json:"start_unix"`
	EndUnix   int64  `json:"end_unix"`
	Rev       int    `json:"rev"`
	Source    string `json:"source"`
}

// ToJSON converts an Instance to its JSON view.
func (inst Instance) ToJSON() InstanceJSON {
	return InstanceJSON{
		ID:        inst.ID,
		Seq:       inst.Seq,
		Start:     FormatTime(inst.Start),
		End:       FormatTime(inst.End),
		StartUnix: inst.Start,
		EndUnix:   inst.End,
		Rev:       inst.Rev,
		Source:    string(inst.Source),
	}
}

// InstancesToJSON converts a slice of instances.
func InstancesToJSON(insts []Instance) []InstanceJSON {
	out := make([]InstanceJSON, 0, len(insts))
	for _, inst := range insts {
		out = append(out, inst.ToJSON())
	}
	return out
}

// ChangeJSON is the JSON view of a Change.
type ChangeJSON struct {
	ID  string       `json:"id"`
	Old InstanceJSON `json:"old"`
	New InstanceJSON `json:"new"`
}

// DiffJSON is the JSON view of a Diff.
type DiffJSON struct {
	Added   []InstanceJSON `json:"added"`
	Removed []InstanceJSON `json:"removed"`
	Changed []ChangeJSON   `json:"changed"`
}

// ToJSON converts a Diff to its JSON view.
func (d Diff) ToJSON() DiffJSON {
	out := DiffJSON{
		Added:   InstancesToJSON(d.Added),
		Removed: InstancesToJSON(d.Removed),
		Changed: make([]ChangeJSON, 0, len(d.Changed)),
	}
	for _, c := range d.Changed {
		out.Changed = append(out.Changed, ChangeJSON{ID: c.ID, Old: c.Old.ToJSON(), New: c.New.ToJSON()})
	}
	return out
}

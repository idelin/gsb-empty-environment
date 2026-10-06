package recur

import (
	"strings"
	"testing"
	"time"
)

const sampleRule = `{
  "rule_id": "standup",
  "start_date": "2026-01-05",
  "weeks": 4,
  "revisions": [
    {"from_seq": 0, "rev": 1, "weekday": "Monday", "minute": 540, "duration_min": 30, "offset_min": 480},
    {"from_seq": 2, "rev": 2, "weekday": "Tuesday", "minute": 600, "duration_min": 45, "offset_min": 480}
  ],
  "exceptions": [
    {"seq": 1, "rev": 1, "action": "cancel"},
    {"seq": 1, "rev": 2, "action": "restore"},
    {"seq": 3, "rev": 1, "action": "override", "weekday": "Friday", "minute": 1020, "duration_min": 30, "offset_min": 480}
  ]
}`

func TestParseRule(t *testing.T) {
	r, err := ParseRule([]byte(sampleRule))
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "standup" || r.Weeks != 4 {
		t.Fatalf("header = %q/%d", r.ID, r.Weeks)
	}
	insts := r.AllInstances()
	if len(insts) != 4 {
		t.Fatalf("instances = %v", ids(insts))
	}
	// seq 0: Mon 09:00 +08:00 = 01:00 UTC
	if insts[0].Start != unix(t, "2026-01-05T01:00:00Z") {
		t.Fatalf("seq0 start = %v", FormatTime(insts[0].Start))
	}
	// seq 1: canceled then restored -> range rule time
	if insts[1].Source != SourceRange || insts[1].Start != unix(t, "2026-01-12T01:00:00Z") {
		t.Fatalf("seq1 = %+v", insts[1])
	}
	// seq 2: revised to Tue 10:00 +08:00 = 02:00 UTC, rev 2
	if insts[2].Rev != 2 || insts[2].Start != unix(t, "2026-01-20T02:00:00Z") || insts[2].End != unix(t, "2026-01-20T02:45:00Z") {
		t.Fatalf("seq2 = %+v", insts[2])
	}
	// seq 3: overridden to Fri 17:00 +08:00 = 09:00 UTC
	if insts[3].Source != SourceOverride || insts[3].Start != unix(t, "2026-01-30T09:00:00Z") {
		t.Fatalf("seq3 = %+v", insts[3])
	}
}

func TestParseRuleErrors(t *testing.T) {
	cases := map[string]string{
		"bad json":        `{`,
		"bad date":        `{"rule_id":"x","start_date":"2026-13-01","weeks":4,"revisions":[{"from_seq":0,"rev":1,"weekday":"Mon","minute":0,"duration_min":1,"offset_min":0}]}`,
		"not monday":      `{"rule_id":"x","start_date":"2026-01-06","weeks":4,"revisions":[{"from_seq":0,"rev":1,"weekday":"Mon","minute":0,"duration_min":1,"offset_min":0}]}`,
		"no base rev":     `{"rule_id":"x","start_date":"2026-01-05","weeks":4,"revisions":[{"from_seq":1,"rev":1,"weekday":"Mon","minute":0,"duration_min":1,"offset_min":0}]}`,
		"bad weekday":     `{"rule_id":"x","start_date":"2026-01-05","weeks":4,"revisions":[{"from_seq":0,"rev":1,"weekday":"noday","minute":0,"duration_min":1,"offset_min":0}]}`,
		"minute range":    `{"rule_id":"x","start_date":"2026-01-05","weeks":4,"revisions":[{"from_seq":0,"rev":1,"weekday":"Mon","minute":1440,"duration_min":1,"offset_min":0}]}`,
		"offset range":    `{"rule_id":"x","start_date":"2026-01-05","weeks":4,"revisions":[{"from_seq":0,"rev":1,"weekday":"Mon","minute":0,"duration_min":1,"offset_min":900}]}`,
		"override fields": `{"rule_id":"x","start_date":"2026-01-05","weeks":4,"revisions":[{"from_seq":0,"rev":1,"weekday":"Mon","minute":0,"duration_min":1,"offset_min":0}],"exceptions":[{"seq":1,"rev":1,"action":"override"}]}`,
		"bad action":      `{"rule_id":"x","start_date":"2026-01-05","weeks":4,"revisions":[{"from_seq":0,"rev":1,"weekday":"Mon","minute":0,"duration_min":1,"offset_min":0}],"exceptions":[{"seq":1,"rev":1,"action":"delete"}]}`,
		"seq range":       `{"rule_id":"x","start_date":"2026-01-05","weeks":4,"revisions":[{"from_seq":0,"rev":1,"weekday":"Mon","minute":0,"duration_min":1,"offset_min":0}],"exceptions":[{"seq":4,"rev":1,"action":"cancel"}]}`,
	}
	for name, doc := range cases {
		if _, err := ParseRule([]byte(doc)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestParseTimeRequiresOffset(t *testing.T) {
	if _, err := ParseTime("2026-01-05 09:00:00"); err == nil {
		t.Fatal("naive time should be rejected")
	}
	v, err := ParseTime("2026-01-05T09:00:00+08:00")
	if err != nil {
		t.Fatal(err)
	}
	if v != unix(t, "2026-01-05T01:00:00Z") {
		t.Fatalf("offset not applied: %v", FormatTime(v))
	}
	if !strings.HasSuffix(FormatTime(v), "Z") {
		t.Fatalf("FormatTime = %v", FormatTime(v))
	}
}

func TestSeqForDate(t *testing.T) {
	r := mustRule(t, "seq", "2026-01-05", 10)
	mustRev(t, r, 0, 1, time.Monday, 0, 1, 0)
	if seq, ok := r.SeqForDate(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)); !ok || seq != 0 {
		t.Fatalf("start date seq = %d,%v", seq, ok)
	}
	if seq, ok := r.SeqForDate(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)); !ok || seq != 7 {
		t.Fatalf("seq = %d,%v want 7,true", seq, ok)
	}
	if _, ok := r.SeqForDate(time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatal("date before start should fail")
	}
	if _, ok := r.SeqForDate(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatal("date beyond horizon should fail")
	}
}

package schedule

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runScript(t *testing.T, script string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := RunCLI(strings.NewReader(script), &out); err != nil {
		t.Fatal(err)
	}
	var resps []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad response line %q: %v", line, err)
		}
		resps = append(resps, m)
	}
	return resps
}

func TestCLIEndToEnd(t *testing.T) {
	script := `
{"op":"new_rule","id":"standup","start":"2026-01-05","weeks":8}
{"op":"add_revision","rule":"standup","rev":1,"from_week":0,"weekday":0,"minute":600,"duration_min":60,"offset_min":480}
{"op":"add_revision","rule":"standup","rev":2,"from_week":2,"weekday":1,"minute":600,"duration_min":60,"offset_min":480}
{"op":"override","rule":"standup","date":"2026-01-26","rev":9,"weekday":5,"minute":1200,"duration_min":30,"offset_min":480}
{"op":"cancel","rule":"standup","week":1}
{"op":"restore","rule":"standup","week":1}
{"op":"query","rule":"standup","from":"2026-01-05T00:00:00Z","to":"2026-02-15T00:00:00Z"}
{"op":"diff","rule":"standup","from_version":1,"to_version":2}
{"op":"query","rule":"standup","from":"bad-time","to":"2026-02-15T00:00:00Z"}
`
	resps := runScript(t, script)
	if len(resps) != 9 {
		t.Fatalf("want 9 responses, got %d", len(resps))
	}
	for i, r := range resps[:8] {
		if r["ok"] != true {
			t.Fatalf("response %d not ok: %v", i, r)
		}
	}
	if resps[8]["ok"] != false {
		t.Fatalf("bad time: want error, got %v", resps[8])
	}

	insts, _ := resps[6]["instances"].([]any)
	if len(insts) != 6 { // weeks 0..5 (override on week 3), all inside window
		t.Fatalf("want 6 instances, got %v", insts)
	}
	first := insts[0].(map[string]any)
	if first["id"] != "standup#0" || first["start"] != "2026-01-05T10:00:00+08:00" {
		t.Fatalf("first instance: %v", first)
	}
	// Week 2 moved to Tuesday by revision 2.
	third := insts[2].(map[string]any)
	if third["id"] != "standup#2" || third["start"] != "2026-01-20T10:00:00+08:00" || third["rev"] != 2.0 {
		t.Fatalf("week 2 instance: %v", third)
	}
	// Week 3 overridden to Saturday 20:00 (+08:00), resolved via date.
	fourth := insts[3].(map[string]any)
	if fourth["id"] != "standup#3" || fourth["start"] != "2026-01-31T20:00:00+08:00" || fourth["rev"] != 9.0 {
		t.Fatalf("overridden instance: %v", fourth)
	}

	changes, _ := resps[7]["changes"].([]any)
	if len(changes) != 6 { // weeks 2..7 moved Monday -> Tuesday
		t.Fatalf("want 6 diff changes, got %v", changes)
	}
	c0 := changes[0].(map[string]any)
	if c0["kind"] != "changed" || c0["id"] != "standup#2" {
		t.Fatalf("first change: %v", c0)
	}
}

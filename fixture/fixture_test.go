package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skewcheck/skew"
)

func TestLoadAndAnalyze(t *testing.T) {
	for _, tc := range []struct {
		path     string
		feasible bool
	}{
		{"feasible_rtt.json", true},
		{"contradiction_rtt.json", false},
		{"disconnected.json", true},
		{"negative_delta.json", true},
	} {
		spec, err := Load(filepath.Join("..", "fixtures", tc.path))
		if err != nil {
			t.Fatalf("%s: load: %v", tc.path, err)
		}
		res, err := spec.Graph().Analyze(spec.Reference, skew.Options{Wide: spec.WideNS()})
		if err != nil {
			t.Fatalf("%s: analyze: %v", tc.path, err)
		}
		if res.Feasible != tc.feasible {
			t.Fatalf("%s: feasible=%v want %v", tc.path, res.Feasible, tc.feasible)
		}
	}
}

func TestTimestampFormats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	content := `{
	  "reference": "a",
	  "events": [
	    {"id":"a1","node":"a","ts":1000},
	    {"id":"b1","node":"b","ts":"-1ms"}
	  ],
	  "edges": [{"from":"a1","to":"b1"}]
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, e := range spec.Events {
		got = append(got, e.TS.TS)
	}
	if got[0] != 1000 || got[1] != -1_000_000 {
		t.Fatalf("timestamps = %v, want [1000 -1000000]", got)
	}
	res, err := spec.Graph().Analyze("a", skew.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible {
		t.Fatal("expected feasible")
	}
}

func TestReplayOrder(t *testing.T) {
	spec, err := Load(filepath.Join("..", "fixtures", "feasible_rtt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	lastTS := int64(-1 << 62)
	err = spec.Replay(func(e skew.Event) error {
		if e.TS < lastTS {
			t.Fatalf("replay out of order at %s", e.ID)
		}
		lastTS = e.TS
		ids = append(ids, e.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c_req", "c_resp", "s_req", "s_resp"} // observed-timestamp order
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("replay order = %v, want %v", ids, want)
	}
}

func TestReplayStop(t *testing.T) {
	spec, err := Load(filepath.Join("..", "fixtures", "disconnected.json"))
	if err != nil {
		t.Fatal(err)
	}
	stopErr := errSentinel
	calls := 0
	err = spec.Replay(func(e skew.Event) error {
		calls++
		if e.Node == "island" {
			return stopErr
		}
		return nil
	})
	if err != stopErr {
		t.Fatalf("replay returned %v, want sentinel", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (two client events then first island)", calls)
	}
}

var errSentinel = newSentinel()

func newSentinel() error { return &sentinelErr{} }

type sentinelErr struct{}

func (*sentinelErr) Error() string { return "stop replay" }

func TestBadFixture(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"empty reference":   `{"events":[{"id":"a1","node":"a","ts":1}]}`,
		"dup event":         `{"reference":"a","events":[{"id":"a1","node":"a","ts":1},{"id":"a1","node":"a","ts":2}]}`,
		"unknown endpoint":  `{"reference":"a","events":[{"id":"a1","node":"a","ts":1}],"edges":[{"from":"a1","to":"x"}]}`,
		"bad timestamp":     `{"reference":"a","events":[{"id":"a1","node":"a","ts":"soon"}]}`,
		"reference missing": `{"reference":"z","events":[{"id":"a1","node":"a","ts":1}]}`,
	}
	for name, body := range cases {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

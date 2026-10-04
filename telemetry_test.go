package logasm

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"testing"
)

// 遥测事件本地合成，可由 JSON 夹具回放并比对事件序列。
type fixture struct {
	Rules struct {
		StartPattern       string `json:"start_pattern"`
		ContinuationIndent bool   `json:"continuation_indent"`
		MaxRecordBytes     int    `json:"max_record_bytes"`
	} `json:"rules"`
	Script []struct {
		Op             string `json:"op"`
		Source         string `json:"source"`
		Epoch          uint64 `json:"epoch"`
		Seq            uint64 `json:"seq"`
		LineNo         int    `json:"line_no"`
		Text           string `json:"text"`
		CorrectsLineNo int    `json:"corrects_line_no"`
	} `json:"script"`
	ExpectEvents []Kind `json:"expect_events"`
}

func TestTelemetryFixtureReplay(t *testing.T) {
	data, err := os.ReadFile("testdata/replay_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}

	rules := Rules{
		Start:              regexp.MustCompile(fx.Rules.StartPattern),
		ContinuationIndent: fx.Rules.ContinuationIndent,
		MaxRecordBytes:     fx.Rules.MaxRecordBytes,
	}
	rec := &Recorder{}
	m, _ := newTestMux(rules, 0, rec)

	for i, op := range fx.Script {
		switch op.Op {
		case "line":
			err := m.Feed(Line{Source: op.Source, Epoch: op.Epoch, Seq: op.Seq,
				LineNo: op.LineNo, Text: op.Text, CorrectsLineNo: -1})
			if err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		case "correct":
			err := m.Feed(Line{Source: op.Source, Epoch: op.Epoch,
				Text: op.Text, CorrectsLineNo: op.CorrectsLineNo})
			if err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		case "flush":
			m.Flush()
		default:
			t.Fatalf("step %d: unknown op %q", i, op.Op)
		}
	}

	if got := rec.Kinds(); !reflect.DeepEqual(got, fx.ExpectEvents) {
		t.Fatalf("event sequence mismatch\n got: %v\nwant: %v", got, fx.ExpectEvents)
	}
}

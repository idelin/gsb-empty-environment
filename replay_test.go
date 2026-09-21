package bridge

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func TestReplayGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := RunReplay(&buf); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "replay.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("replay output changed:\n--- got ---\n%s\n--- want ---\n%s", buf.Bytes(), want)
	}
}

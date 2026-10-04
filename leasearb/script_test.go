package leasearb

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayScripts(t *testing.T) {
	scripts, err := filepath.Glob(filepath.Join("..", "scripts", "*.script"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no scripts found: %v", err)
	}
	for _, path := range scripts {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(path), ".script")
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := NewRunner(&buf).Run(name, string(data)); err != nil {
				t.Fatalf("replay failed: %v\n%s", err, buf.String())
			}
			t.Logf("\n%s", buf.String())
		})
	}
}

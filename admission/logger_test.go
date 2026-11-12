package admission

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONLoggerEmitsOneRecordPerEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewJSONLogger(&buf)
	guard := NewGuard(NewMemoryStore(), logger)

	data := []byte("poison")
	forged := Digest{Hash: strings.Repeat("f", 64), Size: int64(len(data))}
	_, err := guard.AdmitCASWrite(prCtx(), blobResource(forged), data)
	if err == nil {
		t.Fatal("forged digest must be rejected")
	}

	line := strings.TrimSpace(buf.String())
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("expected exactly one JSON line, got %q", line)
	}
	var event Event
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if event.Decision != "deny" || event.Reason != "digest_mismatch" ||
		event.Writer != prIdentity.Name || event.Source != prIdentity.Source {
		t.Fatalf("audit record missing attack context: %+v", event)
	}
}

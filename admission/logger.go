package admission

import (
	"encoding/json"
	"io"
	"sync"
)

// Event is the structured record emitted for every rejected (and selected
// accepted) admission decision. Rejections are always logged so poisoning
// attempts leave an auditable trail.
type Event struct {
	Op           string `json:"op"`
	Decision     string `json:"decision"`
	Code         string `json:"code,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Writer       string `json:"writer,omitempty"`
	Source       string `json:"source,omitempty"`
	Trusted      bool   `json:"trusted"`
	DigestHash   string `json:"digest_hash,omitempty"`
	DigestSize   int64  `json:"digest_size,omitempty"`
	ActionHash   string `json:"action_hash,omitempty"`
	ResourceName string `json:"resource_name,omitempty"`
	Message      string `json:"message,omitempty"`
}

// Logger receives admission events. Implementations must be goroutine-safe.
type Logger interface {
	Log(event Event)
}

// DiscardLogger drops every event.
type DiscardLogger struct{}

// Log implements Logger.
func (DiscardLogger) Log(Event) {}

// JSONLogger emits one JSON object per line and is safe for concurrent use.
type JSONLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLogger builds a Logger writing newline-delimited JSON to w.
func NewJSONLogger(w io.Writer) *JSONLogger {
	return &JSONLogger{w: w}
}

// Log implements Logger.
func (l *JSONLogger) Log(event Event) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(data)
}

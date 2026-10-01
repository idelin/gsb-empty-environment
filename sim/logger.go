package sim

import (
	"fmt"
	"io"
)

// Logger writes tick-prefixed simulation traces. A nil *Logger is valid and
// discards everything, which keeps tests quiet.
type Logger struct {
	W       io.Writer
	Enabled bool
}

func (l *Logger) Log(tick int64, format string, args ...interface{}) {
	if l == nil || !l.Enabled {
		return
	}
	fmt.Fprintf(l.W, "tick=%-6d %s\n", tick, fmt.Sprintf(format, args...))
}

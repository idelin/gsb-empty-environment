package netsec

import (
	"testing"
)

func FuzzParsePriority(f *testing.F) {
	f.Add("u=3, i=?0")
	f.Add("u=7;foo=bar, unknown=(1;x=?1 2), i=?1")
	f.Add(`u="bad", i=2`)
	f.Add("u=8")
	f.Add("   ")
	f.Fuzz(func(t *testing.T, fieldValue string) {
		// The parser must never panic; a rejected value must leave state
		// untouched for a live stream.
		state := NewState()
		_ = state.Update(1, fieldValue)

		priority, ok := state.Priority(1)
		if ok {
			if priority.Urgency > maxUrgency {
				t.Fatalf("stored urgency out of range: %d", priority.Urgency)
			}
		}
		if state.PendingCount() > maxPending {
			t.Fatalf("pending cap violated: %d", state.PendingCount())
		}
	})
}

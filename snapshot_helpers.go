package netsec

// AuditEventsLocked returns a copy of the audit trail while the caller holds
// s.mu. ExportSnapshot needs it because AuditEvents itself takes the lock.
func (s *State) AuditEventsLocked() []AuditEvent {
	out := make([]AuditEvent, len(s.events))
	copy(out, s.events)
	return out
}

func cloneHolderIndex(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for id, index := range in {
		out[id] = index
	}
	return out
}

func cloneHolderKeys(in map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for id, key := range in {
		out[id] = append([]byte(nil), key...)
	}
	return out
}

func cloneHolderSeen(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for id, seen := range in {
		out[id] = seen
	}
	return out
}

package logrec

// Entry is a single replicated log record.
type Entry struct {
	Index uint64
	Data  string
}

// Log is the leader's replicated log with a recycled (deleted) prefix.
// Entries with Index <= base are gone forever; entries[0].Index == base+1
// whenever the log is non-empty.
type Log struct {
	base    uint64
	entries []Entry
}

// Base is the recycled prefix length: the highest deleted index.
func (l *Log) Base() uint64 { return l.base }

// FirstAvailable is the smallest index still present in the log.
func (l *Log) FirstAvailable() uint64 { return l.base + 1 }

// LastIndex is the highest index ever appended.
func (l *Log) LastIndex() uint64 { return l.base + uint64(len(l.entries)) }

// Append appends a new entry and returns it.
func (l *Log) Append(data string) Entry {
	e := Entry{Index: l.LastIndex() + 1, Data: data}
	l.entries = append(l.entries, e)
	return e
}

// Recycle deletes every entry with Index <= prefix. It never moves the base
// backwards and never deletes beyond the last appended entry.
func (l *Log) Recycle(prefix uint64) {
	if prefix <= l.base {
		return
	}
	if prefix > l.LastIndex() {
		prefix = l.LastIndex()
	}
	cut := prefix - l.base
	l.entries = append([]Entry(nil), l.entries[cut:]...)
	l.base = prefix
}

package trijoin

import (
	"sort"
)

type recordID struct {
	stream Stream
	key    string
	rowID  int64
}

type stateRow struct {
	id      recordID
	time    int64
	payload any
	count   int64
}

func activeCount(count int64) int64 {
	if count > 0 {
		return count
	}
	return 0
}

type streamState struct {
	rows    map[recordID]*stateRow
	byKey   map[string][]*stateRow
	expired map[string][]*stateRow
}

func newStreamState() *streamState {
	return &streamState{
		rows:    make(map[recordID]*stateRow),
		byKey:   make(map[string][]*stateRow),
		expired: make(map[string][]*stateRow),
	}
}

func rowLess(a, b *stateRow) bool {
	if a.time != b.time {
		return a.time < b.time
	}
	return a.id.rowID < b.id.rowID
}

func (s *streamState) insert(row *stateRow) {
	s.rows[row.id] = row
	list := s.byKey[row.id.key]
	i := sort.Search(len(list), func(i int) bool {
		return !rowLess(list[i], row)
	})
	list = append(list, nil)
	copy(list[i+1:], list[i:])
	list[i] = row
	s.byKey[row.id.key] = list
}

func (s *streamState) rangeActive(key string, low, high int64) []*stateRow {
	if low > high {
		return nil
	}
	out := rangeActiveIn(s.byKey[key], low, high, nil)
	// Expired rows are frozen and no longer count as operator state. They are
	// still considered when an accepted event is between its stream's retained
	// boundary and the watermark. This keeps deltas consistent with the full
	// accepted changelog history without making the expired rows active state.
	out = rangeActiveIn(s.expired[key], low, high, out)
	return out
}

func rangeActiveIn(list []*stateRow, low, high int64, out []*stateRow) []*stateRow {
	first := sort.Search(len(list), func(i int) bool {
		return list[i].time >= low
	})
	last := sort.Search(len(list), func(i int) bool {
		return list[i].time > high
	})
	for _, row := range list[first:last] {
		if row.count > 0 {
			out = append(out, row)
		}
	}
	return out
}

// clean removes rows whose event time is strictly below the boundary. The
// boundary is monotonically non-decreasing, so each key's time-sorted list
// loses a prefix.
func (s *streamState) clean(boundary int64) int {
	removed := 0
	for key, list := range s.byKey {
		cut := sort.Search(len(list), func(i int) bool {
			return list[i].time >= boundary
		})
		for _, row := range list[:cut] {
			delete(s.rows, row.id)
			s.expired[key] = append(s.expired[key], row)
			removed++
		}
		if cut == 0 {
			continue
		}
		if cut == len(list) {
			delete(s.byKey, key)
			continue
		}
		remaining := make([]*stateRow, len(list)-cut)
		copy(remaining, list[cut:])
		s.byKey[key] = remaining
	}
	return removed
}

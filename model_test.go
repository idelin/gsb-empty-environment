package prefixdict

import (
	"sort"
	"strings"
)

// model is the naive reference: it keeps per-source contributions and
// answers every query by full-table aggregation plus a total sort.
type model struct {
	bySource map[string]map[string]int64 // source -> word -> weight
}

func newModel() *model {
	return &model{bySource: make(map[string]map[string]int64)}
}

func (m *model) clone() *model {
	cp := newModel()
	for source, words := range m.bySource {
		wc := make(map[string]int64, len(words))
		for word, weight := range words {
			wc[word] = weight
		}
		cp.bySource[source] = wc
	}
	return cp
}

func (m *model) set(source, word string, weight int64) {
	words, ok := m.bySource[source]
	if !ok {
		words = make(map[string]int64)
		m.bySource[source] = words
	}
	words[word] = weight
}

func (m *model) replaceSource(source string, contribs map[string]int64) {
	delete(m.bySource, source)
	for word, weight := range contribs {
		m.set(source, word, weight)
	}
}

func (m *model) removeSource(source string) {
	delete(m.bySource, source)
}

// topK aggregates every source over the full table, keeps words that still
// have at least one contributing source (zero totals included), filters by
// prefix, and sorts by weight descending with byte-order tie-breaks.
func (m *model) topK(prefix string, k int) []Entry {
	if k <= 0 {
		return nil
	}
	totals := make(map[string]int64)
	for _, words := range m.bySource {
		for word, weight := range words {
			totals[word] += weight
		}
	}
	entries := make([]Entry, 0, len(totals))
	for word, total := range totals {
		if strings.HasPrefix(word, prefix) {
			entries = append(entries, Entry{Word: word, Weight: total})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Weight != entries[j].Weight {
			return entries[i].Weight > entries[j].Weight
		}
		return entries[i].Word < entries[j].Word
	})
	if len(entries) > k {
		entries = entries[:k]
	}
	return entries
}

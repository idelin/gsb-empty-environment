package netsec

import (
	"sort"
	"strings"
	"sync"
)

// PartitionKey identifies one cached materialized partition.
type PartitionKey struct {
	// ViewDigest is the ViewDef.DefinitionDigest.
	ViewDigest string
	// PolicyDigest and SubjectClass are empty for OwnerSnapshot entries.
	PolicyDigest string
	SubjectClass string
}

func ownerKey(viewDigest string) PartitionKey {
	return PartitionKey{ViewDigest: viewDigest}
}

func partitionKey(viewDigest, policyDigest, subjectClass string) PartitionKey {
	return PartitionKey{
		ViewDigest:   viewDigest,
		PolicyDigest: policyDigest,
		SubjectClass: subjectClass,
	}
}

func (k PartitionKey) String() string {
	return k.ViewDigest + "|" + k.PolicyDigest + "|" + k.SubjectClass
}

// GroupKey is the canonical rendering of a grouping-key tuple.
type GroupKey string

// ResultRow is one materialized output tuple.
type ResultRow struct {
	Group  GroupKey
	Values map[string]int64
}

// Entry is one complete, immutable cache partition.
type Entry struct {
	Key PartitionKey
	// Bindings make the entry unusable the moment any security input changes.
	SourceID      string
	SourceVersion int64
	PolicyEpoch   int64
	SubjectClass  string
	SubjectRole   string
	PolicyDigest  string
	RefreshLSN    int64
	Rows          []ResultRow
	// Complete is false only for a staging entry captured mid-refresh by the
	// interruption tests; published entries are always complete.
	Complete bool
}

// cache holds all entries and serializes publication so a swap is atomic with
// respect to readers.
type cache struct {
	mu      sync.RWMutex
	entries map[PartitionKey]*Entry
}

func newCache() *cache {
	return &cache{entries: map[PartitionKey]*Entry{}}
}

// lookup returns the current entry without any validity interpretation.
func (c *cache) lookup(k PartitionKey) (*Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[k]
	return e, ok
}

// swap publishes a fully built entry, replacing any previous one. The swap is
// the only mutation visible to readers, so partial staging state never leaks.
func (c *cache) swap(e *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[e.Key] = e
}

// invalidateView drops every entry bound to the view (used when the source is
// rebuilt under the same name).
func (c *cache) invalidateView(viewDigest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if k.ViewDigest == viewDigest {
			delete(c.entries, k)
		}
	}
}

func (c *cache) snapshot() map[PartitionKey]*Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[PartitionKey]*Entry, len(c.entries))
	for k, v := range c.entries {
		out[k] = v
	}
	return out
}

// makeGroupKey renders grouping column values in schema-declared order.
func makeGroupKey(groupBy []string, row Row) (GroupKey, error) {
	if len(groupBy) == 0 {
		return GroupKey("*"), nil
	}
	parts := make([]string, len(groupBy))
	for i, col := range groupBy {
		v, err := asInt64(row[col])
		if err != nil {
			return "", err
		}
		parts[i] = itoa(v)
	}
	return GroupKey(strings.Join(parts, "/")), nil
}

func sortedGroupKeys(m map[GroupKey]*groupAccum) []GroupKey {
	keys := make([]GroupKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

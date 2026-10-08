package snapaudit

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Reason describes why a Violation was reported.
type Reason int

const (
	// ReasonModified: an immutable value changed.
	ReasonModified Reason = iota
	// ReasonAdded: a key or index appeared without a matching
	// AllowAdd rule.
	ReasonAdded
	// ReasonRemoved: a key or index disappeared without a matching
	// AllowDelete rule.
	ReasonRemoved
)

func (r Reason) String() string {
	switch r {
	case ReasonModified:
		return "modified"
	case ReasonAdded:
		return "added"
	case ReasonRemoved:
		return "removed"
	}
	return "unknown"
}

// Violation is a single policy breach. Path is the shallowest path
// at which the breach can be attributed; Old and New hold the values
// at that path in the respective snapshots (nil when absent).
type Violation struct {
	Path   string
	Reason Reason
	Old    interface{}
	New    interface{}
}

// Compare diffs two snapshots and returns every policy violation,
// ordered shallowest path first (ties keep a deterministic,
// lexicographic-by-segment traversal order).
//
// The snapshots must be composed of JSON-like values:
// map[string]interface{}, []interface{}, string, float64, bool and
// nil, as produced by encoding/json. Use CompareJSON to decode and
// compare raw JSON documents in one step.
//
// Comparison rules:
//
//   - A difference at or below an immutable path is reported once,
//     at the immutable path; deeper duplicates are suppressed.
//   - Replacing a parent object does not bypass protection: the
//     replaced subtree is checked recursively for immutable
//     descendants, and every added or removed leaf must be covered
//     by AllowAdd or AllowDelete.
//   - Scalar modifications are allowed unless the path is immutable.
//   - A change of kind (e.g. object to scalar) decomposes into a
//     removal of the old subtree plus an addition of the new one.
//   - Slices are compared index by index; the index is a path
//     segment and may be matched by the single-level wildcard.
func Compare(old, new interface{}, policy Policy) []Violation {
	c := &checker{policy: policy}
	c.diff(nil, old, new)
	sort.SliceStable(c.violations, func(i, j int) bool {
		return len(splitPath(c.violations[i].Path)) < len(splitPath(c.violations[j].Path))
	})
	return c.violations
}

// First returns the shallowest violation between two snapshots, or
// nil when the diff complies with the policy.
func First(old, new interface{}, policy Policy) *Violation {
	violations := Compare(old, new, policy)
	if len(violations) == 0 {
		return nil
	}
	return &violations[0]
}

// CompareJSON decodes two JSON documents and compares them like
// Compare.
func CompareJSON(oldJSON, newJSON []byte, policy Policy) ([]Violation, error) {
	var old, new interface{}
	if err := json.Unmarshal(oldJSON, &old); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(newJSON, &new); err != nil {
		return nil, err
	}
	return Compare(old, new, policy), nil
}

type checker struct {
	policy     Policy
	violations []Violation
}

// diff compares old and new at path, both present in their
// respective snapshots.
func (c *checker) diff(path []string, old, new interface{}) {
	if reflect.DeepEqual(old, new) {
		return
	}
	if matchAny(c.policy.Immutable, path) {
		c.report(ReasonModified, path, old, new)
		return
	}
	if oldMap, ok := old.(map[string]interface{}); ok {
		if newMap, ok := new.(map[string]interface{}); ok {
			c.diffMaps(path, oldMap, newMap)
			return
		}
	}
	if oldSlice, ok := old.([]interface{}); ok {
		if newSlice, ok := new.([]interface{}); ok {
			c.diffSlices(path, oldSlice, newSlice)
			return
		}
	}
	// Replacement: a scalar change, or a change of kind. Composite
	// subtrees on either side decompose into per-leaf removals and
	// additions so protected descendants are still checked.
	if isComposite(old) {
		c.removed(path, old)
	}
	if isComposite(new) {
		c.added(path, new)
	}
}

func (c *checker) diffMaps(path []string, old, new map[string]interface{}) {
	for _, key := range sortedKeys(old) {
		if _, found := new[key]; !found {
			c.removed(childPath(path, key), old[key])
		}
	}
	for _, key := range sortedKeys(new) {
		oldValue, found := old[key]
		if !found {
			c.added(childPath(path, key), new[key])
			continue
		}
		c.diff(childPath(path, key), oldValue, new[key])
	}
}

func (c *checker) diffSlices(path []string, old, new []interface{}) {
	shared := len(old)
	if len(new) < shared {
		shared = len(new)
	}
	for i := 0; i < shared; i++ {
		c.diff(childPath(path, strconv.Itoa(i)), old[i], new[i])
	}
	for i := shared; i < len(old); i++ {
		c.removed(childPath(path, strconv.Itoa(i)), old[i])
	}
	for i := shared; i < len(new); i++ {
		c.added(childPath(path, strconv.Itoa(i)), new[i])
	}
}

// removed checks a value present in the old snapshot but absent at
// path in the new one. The subtree is expanded until an AllowDelete
// (or violated Immutable) rule matches, leaf by leaf.
func (c *checker) removed(path []string, value interface{}) {
	if matchAny(c.policy.Immutable, path) {
		c.report(ReasonRemoved, path, value, nil)
		return
	}
	if matchAny(c.policy.AllowDelete, path) {
		return
	}
	switch composite := value.(type) {
	case map[string]interface{}:
		for _, key := range sortedKeys(composite) {
			c.removed(childPath(path, key), composite[key])
		}
	case []interface{}:
		for i, element := range composite {
			c.removed(childPath(path, strconv.Itoa(i)), element)
		}
	default:
		c.report(ReasonRemoved, path, value, nil)
	}
}

// added checks a value present in the new snapshot but absent at
// path in the old one, mirroring removed with AllowAdd.
func (c *checker) added(path []string, value interface{}) {
	if matchAny(c.policy.Immutable, path) {
		c.report(ReasonAdded, path, nil, value)
		return
	}
	if matchAny(c.policy.AllowAdd, path) {
		return
	}
	switch composite := value.(type) {
	case map[string]interface{}:
		for _, key := range sortedKeys(composite) {
			c.added(childPath(path, key), composite[key])
		}
	case []interface{}:
		for i, element := range composite {
			c.added(childPath(path, strconv.Itoa(i)), element)
		}
	default:
		c.report(ReasonAdded, path, nil, value)
	}
}

func (c *checker) report(reason Reason, path []string, old, new interface{}) {
	c.violations = append(c.violations, Violation{
		Path:   strings.Join(path, "."),
		Reason: reason,
		Old:    old,
		New:    new,
	})
}

func isComposite(value interface{}) bool {
	switch value.(type) {
	case map[string]interface{}, []interface{}:
		return true
	}
	return false
}

// childPath returns path extended by one segment. The result never
// aliases the backing array of path, so callers may keep appending
// to path afterwards.
func childPath(path []string, segment string) []string {
	child := make([]string, len(path)+1)
	copy(child, path)
	child[len(path)] = segment
	return child
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

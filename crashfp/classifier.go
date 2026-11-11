package crashfp

import (
	"fmt"
	"sort"
)

// Reason explains why two equivalence classes are retained as candidates.
type Reason string

const (
	// ReasonTruncatedSuffix means the shorter stack is truncated and its
	// known frames are an exact suffix of the other stack.
	ReasonTruncatedSuffix Reason = "truncated_suffix"
	// ReasonCommonSuffix means both stacks share a non-empty known suffix
	// but diverge before it, so neither identity nor compatibility is sure.
	ReasonCommonSuffix Reason = "common_suffix"
)

// Candidate is an uncertain, deliberately non-merged association between two
// equivalence classes. Candidates are never promoted to merges automatically.
type Candidate struct {
	// ClassA and ClassB are the stable IDs of the two related classes.
	// ClassA sorts before ClassB, independent of insertion order.
	ClassA string
	ClassB string
	// Reason is either a strict truncated-suffix fit or a common suffix.
	Reason Reason
	// SharedFrames is the number of identical trailing frames.
	SharedFrames int
}

// Class is one equivalence class of structurally identical normalized stacks.
type Class struct {
	// ID is stable across insertion orders: the lexicographically smallest
	// report ID in the equivalence class.
	ID string
	// Fingerprint is the shared normalized-stack fingerprint.
	Fingerprint string
	// ReportIDs lists every member, sorted lexicographically.
	ReportIDs []string
}

// Options controls uncertain candidate matching.
type Options struct {
	// MinSuffixFrames is the minimum shared trailing-frame count required
	// to retain a candidate. Zero defaults to one matching frame.
	MinSuffixFrames int
}

type entry struct {
	id          string
	exception   string
	stack       *Stack
	fingerprint string
	parent      int
	rank        int
}

type candidateRecord struct {
	leftFingerprint  string
	rightFingerprint string
	reason           Reason
	sharedFrames     int
}

// Classifier accumulates crash reports and produces order-independent classes
// and candidate associations.
type Classifier struct {
	entries       []*entry
	byID          map[string]int
	byFingerprint map[string]int
	candidates    map[[2]string]candidateRecord
	minSuffix     int
}

// NewClassifier creates a Classifier. A nil Options value uses defaults.
func NewClassifier(options *Options) *Classifier {
	minimum := 1
	if options != nil && options.MinSuffixFrames > 0 {
		minimum = options.MinSuffixFrames
	}
	return &Classifier{
		byID:          make(map[string]int),
		byFingerprint: make(map[string]int),
		candidates:    make(map[[2]string]candidateRecord),
		minSuffix:     minimum,
	}
}

// Add normalizes when needed and inserts one report. Identical normalized
// stacks are joined into the same equivalence class; uncertain suffix
// relationships are retained as candidates without joining classes.
func (c *Classifier) Add(report Report) error {
	if report.ID == "" {
		return fmt.Errorf("%w: missing report id", ErrInvalidReport)
	}
	if _, exists := c.byID[report.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateReport, report.ID)
	}

	stack := report.Stack
	if stack == nil {
		normalized, err := Normalize(report.Frames, report.Modules)
		if err != nil {
			return err
		}
		stack = normalized
	} else {
		if len(stack.Frames) == 0 {
			return ErrEmptyStack
		}
		stack = cloneStack(stack)
	}

	fingerprint := stack.Fingerprint()
	current := &entry{
		id:          report.ID,
		exception:   report.Exception,
		stack:       stack,
		fingerprint: fingerprint,
		parent:      len(c.entries),
	}
	index := len(c.entries)
	c.entries = append(c.entries, current)
	c.byID[report.ID] = index

	if existing, ok := c.byFingerprint[fingerprint]; ok {
		c.union(index, existing)
	} else {
		c.byFingerprint[fingerprint] = index
	}

	c.collectCandidates(index)
	return nil
}

// AddStack inserts an already normalized report.
func (c *Classifier) AddStack(id, exception string, stack *Stack) error {
	if stack == nil {
		return fmt.Errorf("%w: nil stack", ErrInvalidReport)
	}
	return c.Add(Report{ID: id, Exception: exception, Stack: cloneStack(stack)})
}

func (c *Classifier) collectCandidates(index int) {
	current := c.entries[index]
	for otherIndex, other := range c.entries {
		if otherIndex == index || c.sameRoot(index, otherIndex) {
			continue
		}

		shared := commonSuffixLength(current.stack.Frames, other.stack.Frames)
		if shared < c.minSuffix {
			continue
		}

		reason := candidateReason(current.stack, other.stack, shared)
		if reason == "" {
			continue
		}

		// The unordered pair needs a canonical map key; fingerprint order
		// is only a storage convention and does not imply display order.
		firstFingerprint, secondFingerprint := current.fingerprint, other.fingerprint
		if firstFingerprint > secondFingerprint {
			firstFingerprint, secondFingerprint = secondFingerprint, firstFingerprint
		}
		key := [2]string{firstFingerprint, secondFingerprint}
		if _, exists := c.candidates[key]; exists {
			continue
		}
		c.candidates[key] = candidateRecord{
			leftFingerprint:  firstFingerprint,
			rightFingerprint: secondFingerprint,
			reason:           reason,
			sharedFrames:     shared,
		}
	}
}

func candidateReason(a, b *Stack, shared int) Reason {
	aSuffix := shared == len(a.Frames)
	bSuffix := shared == len(b.Frames)

	// A strict suffix is accepted as compatibility evidence only when the
	// shorter observation is explicitly truncated. Two complete stacks with
	// a strict suffix relation are not equivalent and receive no such hint.
	if aSuffix || bSuffix {
		if (aSuffix && a.Truncated) || (bSuffix && b.Truncated) {
			return ReasonTruncatedSuffix
		}
		return ""
	}

	// A shared tail with an earlier divergence remains an uncertain lead.
	if shared > 0 {
		return ReasonCommonSuffix
	}
	return ""
}

func (c *Classifier) find(index int) int {
	root := index
	for c.entries[root].parent != root {
		root = c.entries[root].parent
	}
	for c.entries[index].parent != index {
		next := c.entries[index].parent
		c.entries[index].parent = root
		index = next
	}
	return root
}

func (c *Classifier) union(a, b int) {
	left := c.find(a)
	right := c.find(b)
	if left == right {
		return
	}

	if c.entries[left].rank < c.entries[right].rank {
		left, right = right, left
	}
	c.entries[right].parent = left
	if c.entries[left].rank == c.entries[right].rank {
		c.entries[left].rank++
	}
}

func (c *Classifier) sameRoot(a, b int) bool {
	return c.find(a) == c.find(b)
}

// Classes returns the equivalence partition. The result is sorted by class ID;
// each class's member IDs are sorted as well.
func (c *Classifier) Classes() []Class {
	groups := make(map[int][]string)
	for _, item := range c.entries {
		root := c.find(c.byID[item.id])
		groups[root] = append(groups[root], item.id)
	}

	classes := make([]Class, 0, len(groups))
	for root, ids := range groups {
		sort.Strings(ids)
		classes = append(classes, Class{
			ID:          ids[0],
			Fingerprint: c.entries[root].fingerprint,
			ReportIDs:   ids,
		})
	}

	sort.Slice(classes, func(i, j int) bool {
		return classes[i].ID < classes[j].ID
	})
	return classes
}

// ClassOf returns the stable class ID for one report, or an empty string for
// an unknown report.
func (c *Classifier) ClassOf(id string) string {
	index, ok := c.byID[id]
	if !ok {
		return ""
	}

	root := c.find(index)
	minimum := c.entries[root].id
	for _, item := range c.entries {
		if c.find(c.byID[item.id]) == root && item.id < minimum {
			minimum = item.id
		}
	}
	return minimum
}

// Candidates returns all uncertain class-level associations, sorted by class
// IDs. Associations are deduplicated per distinct fingerprint pair.
func (c *Classifier) Candidates() []Candidate {
	result := make([]Candidate, 0, len(c.candidates))
	for _, record := range c.candidates {
		leftIndex, leftOK := c.byFingerprint[record.leftFingerprint]
		rightIndex, rightOK := c.byFingerprint[record.rightFingerprint]
		if !leftOK || !rightOK {
			continue
		}
		classA := c.ClassOf(c.entries[leftIndex].id)
		classB := c.ClassOf(c.entries[rightIndex].id)
		if classA > classB {
			classA, classB = classB, classA
		}
		result = append(result, Candidate{
			ClassA:       classA,
			ClassB:       classB,
			Reason:       record.reason,
			SharedFrames: record.sharedFrames,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ClassA != result[j].ClassA {
			return result[i].ClassA < result[j].ClassA
		}
		return result[i].ClassB < result[j].ClassB
	})
	return result
}

// ReportCount returns the number of inserted reports.
func (c *Classifier) ReportCount() int {
	return len(c.entries)
}

// Exception returns the stored exception name for a report.
func (c *Classifier) Exception(id string) (string, bool) {
	index, ok := c.byID[id]
	if !ok {
		return "", false
	}
	return c.entries[index].exception, true
}

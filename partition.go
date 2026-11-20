package projection

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type constraintSet struct {
	labels     []Label
	allowed    [][]bool
	forbidden  [][]bool
	legalBlock []bool
}

func buildConstraints(m *machine, config Config) (*constraintSet, error) {
	labelSet := make(map[Label]struct{}, len(m.labels)+len(config.Labels))
	for _, label := range m.labels {
		labelSet[label] = struct{}{}
	}
	for _, label := range config.Labels {
		if label == "" {
			return nil, fmt.Errorf("projection: empty configured label")
		}
		labelSet[label] = struct{}{}
	}

	labels := sortedLabelsFromSet(labelSet)
	if len(labels) > 12 {
		return nil, fmt.Errorf("projection: %d original labels exceed the limit of 12", len(labels))
	}
	index := make(map[Label]int, len(labels))
	for i, label := range labels {
		index[label] = i
	}

	allowed := make([][]bool, len(labels))
	forbidden := make([][]bool, len(labels))
	for i := range labels {
		allowed[i] = make([]bool, len(labels))
		forbidden[i] = make([]bool, len(labels))
		allowed[i][i] = true
	}

	for _, pair := range config.AllowedMerges {
		a, b := pair[0], pair[1]
		if a == "" || b == "" {
			return nil, fmt.Errorf("projection: empty allowed merge label")
		}
		ia, okA := index[a]
		ib, okB := index[b]
		if !okA || !okB {
			return nil, fmt.Errorf("projection: allowed merge references unknown label %q or %q", a, b)
		}
		allowed[ia][ib] = true
		allowed[ib][ia] = true
	}
	for _, pair := range config.ForbiddenMerges {
		a, b := pair[0], pair[1]
		if a == "" || b == "" {
			return nil, fmt.Errorf("projection: empty forbidden merge label")
		}
		ia, okA := index[a]
		ib, okB := index[b]
		if !okA || !okB {
			return nil, fmt.Errorf("projection: forbidden merge references unknown label %q or %q", a, b)
		}
		if ia == ib {
			return nil, fmt.Errorf("projection: business label %q cannot be distinguished from itself", a)
		}
		forbidden[ia][ib] = true
		forbidden[ib][ia] = true
	}

	// Merge hints are not trusted to be transitively closed.
	for k := range labels {
		for i := range labels {
			if !allowed[i][k] {
				continue
			}
			for j := range labels {
				if allowed[k][j] {
					allowed[i][j] = true
				}
			}
		}
	}

	legalBlock := make([]bool, 1<<len(labels))
	for mask := 0; mask < len(legalBlock); mask++ {
		ok := true
		for i := 0; i < len(labels) && ok; i++ {
			if mask&(1<<i) == 0 {
				continue
			}
			for j := i + 1; j < len(labels); j++ {
				if mask&(1<<j) == 0 {
					continue
				}
				if !allowed[i][j] || forbidden[i][j] {
					ok = false
					break
				}
			}
		}
		legalBlock[mask] = ok
	}

	return &constraintSet{
		labels:     labels,
		allowed:    allowed,
		forbidden:  forbidden,
		legalBlock: legalBlock,
	}, nil
}

func projectionFromBlocks(labels []Label, blocks []uint16) Projection {
	projection := Projection{Mapping: make(map[Label]Label, len(labels))}
	for _, block := range blocks {
		representative := -1
		for i := range labels {
			if block&(1<<i) != 0 {
				representative = i
				break
			}
		}
		if representative < 0 {
			continue
		}
		for i := range labels {
			if block&(1<<i) != 0 {
				projection.Mapping[labels[i]] = labels[representative]
			}
		}
	}
	return projection
}

func encodeProjection(projection Projection, labels []Label) string {
	ordered := append([]Label(nil), labels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	parts := make([]string, 0, len(ordered))
	for _, label := range ordered {
		parts = append(parts, string(label)+"="+string(projection.Public(label)))
	}
	return strings.Join(parts, ";")
}

func encodeBlocks(labels []Label, blocks []uint16) string {
	return encodeProjection(projectionFromBlocks(labels, blocks), labels)
}

func canonicalBlocks(blocks []uint16) []uint16 {
	out := append([]uint16(nil), blocks...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func mergeBlocks(blocks []uint16, left, right int) []uint16 {
	out := make([]uint16, 0, len(blocks)-1)
	for i, block := range blocks {
		switch i {
		case left:
			out = append(out, block|blocks[right])
		case right:
			continue
		default:
			out = append(out, block)
		}
	}
	return canonicalBlocks(out)
}

type synthesisState struct {
	m            *machine
	constraints  *constraintSet
	bestCount    int
	bestEncoding string
	best         Projection
	bestSet      bool
	visited      map[string]struct{}
}

func (s *synthesisState) visit(blocks []uint16) {
	blocks = canonicalBlocks(blocks)
	encoding := encodeBlocks(s.constraints.labels, blocks)
	if _, ok := s.visited[encoding]; ok {
		return
	}
	s.visited[encoding] = struct{}{}

	count := len(blocks)
	if count < s.bestCount {
		return
	}

	projection := projectionFromBlocks(s.constraints.labels, blocks)
	_, failure, err := evaluateSafety(s.m, projection, false, s.constraints.labels)
	if err != nil {
		return
	}
	if failure == nil {
		if count > s.bestCount || (count == s.bestCount && (s.bestEncoding == "" || encoding < s.bestEncoding)) {
			s.bestCount = count
			s.bestEncoding = encoding
			s.best = projection
			s.bestSet = true
		}
		// Every coarsening has fewer categories and cannot improve the optimum.
		return
	}
	if count <= s.bestCount {
		return
	}

	for left := 0; left < len(blocks); left++ {
		for right := left + 1; right < len(blocks); right++ {
			union := blocks[left] | blocks[right]
			if !s.constraints.legalBlock[union] {
				continue
			}
			s.visit(mergeBlocks(blocks, left, right))
		}
	}
}

// Synthesize finds the safe projection with the greatest number of retained
// categories.  Among ties it chooses the lexicographically smallest canonical
// encoding over sorted original labels.
func Synthesize(protocol Protocol, config Config) (Result, error) {
	m, err := compileProtocol(protocol)
	if err != nil {
		return Result{}, err
	}
	constraints, err := buildConstraints(m, config)
	if err != nil {
		return Result{}, err
	}

	initial := make([]uint16, len(constraints.labels))
	for i := range constraints.labels {
		initial[i] = 1 << i
	}
	search := &synthesisState{
		m:           m,
		constraints: constraints,
		visited:     make(map[string]struct{}),
	}
	if len(initial) == 0 {
		search.best = Projection{Mapping: map[Label]Label{}}
		search.bestEncoding = ""
		search.bestCount = 0
		search.bestSet = true
	} else {
		search.visit(initial)
		if !search.bestSet {
			return Result{}, ErrNoSafeProjection
		}
	}

	safety, failure, err := evaluateSafety(m, search.best, true, constraints.labels)
	if err != nil {
		return Result{}, err
	}
	if failure != nil {
		return Result{}, fmt.Errorf("projection: optimum proof failed: %w", failure)
	}
	optimality, err := buildOptimality(m, constraints, search.bestCount, search.bestEncoding)
	if err != nil {
		return Result{}, err
	}

	certificate := Certificate{
		Encoding:      search.bestEncoding,
		CategoryCount: search.bestCount,
		Projection:    search.best.Clone(),
		Safety:        *safety,
		Optimality:    optimality,
	}
	return Result{
		Projection:    search.best.Clone(),
		CategoryCount: search.bestCount,
		Certificate:   certificate,
	}, nil
}

type xorDigest struct {
	xor   [sha256.Size]byte
	count int
}

func (d *xorDigest) add(encoding string, categoryCount int) {
	sum := sha256.Sum256([]byte(encoding + "\x00" + strconv.Itoa(categoryCount)))
	for i := range d.xor {
		d.xor[i] ^= sum[i]
	}
	d.count++
}

func (d *xorDigest) finish() string {
	material := make([]byte, 0, len("projection-refutations-v1")+sha256.Size+32)
	material = append(material, []byte("projection-refutations-v1")...)
	material = append(material, 0)
	material = append(material, []byte(strconv.Itoa(d.count))...)
	material = append(material, 0)
	material = append(material, d.xor[:]...)
	sum := sha256.Sum256(material)
	return fmt.Sprintf("%x", sum[:])
}

func enumerateLegalPartitions(labels []Label, legalBlock []bool, minBlocks int, callback func([]uint16)) {
	if minBlocks <= 0 {
		minBlocks = 1
	}
	var blocks []uint16
	var walk func(index int)
	walk = func(index int) {
		if len(blocks)+(len(labels)-index) < minBlocks {
			return
		}
		if index == len(labels) {
			if len(blocks) >= minBlocks {
				callback(append([]uint16(nil), blocks...))
			}
			return
		}
		bit := uint16(1) << index
		for i := range blocks {
			union := blocks[i] | bit
			if !legalBlock[union] {
				continue
			}
			blocks[i] = union
			walk(index + 1)
			blocks[i] &^= bit
		}
		blocks = append(blocks, bit)
		walk(index + 1)
		blocks = blocks[:len(blocks)-1]
	}
	if len(labels) == 0 {
		if minBlocks <= 0 {
			callback(nil)
		}
		return
	}
	walk(0)
}

func buildOptimality(m *machine, constraints *constraintSet, bestCount int, bestEncoding string) (OptimalityCertificate, error) {
	var digest xorDigest
	var samples []Refutation
	safeFound := false

	enumerateLegalPartitions(constraints.labels, constraints.legalBlock, bestCount+1, func(blocks []uint16) {
		if safeFound {
			return
		}
		projection := projectionFromBlocks(constraints.labels, blocks)
		encoding := encodeBlocks(constraints.labels, blocks)
		_, failure, err := evaluateSafety(m, projection, false, constraints.labels)
		if err != nil {
			return
		}
		if failure == nil {
			safeFound = true
			return
		}
		digest.add(encoding, len(blocks))
		if len(samples) < MaxRefutationWitnesses {
			samples = append(samples, Refutation{
				Encoding:      encoding,
				CategoryCount: len(blocks),
				Projection:    projection,
				Witness:       failure,
			})
		}
	})
	if safeFound {
		return OptimalityCertificate{}, fmt.Errorf("projection: exhaustive search missed a safe projection")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Encoding < samples[j].Encoding })

	return OptimalityCertificate{
		CategoryCount:     bestCount,
		CanonicalEncoding: bestEncoding,
		AboveBest:         digest.count,
		Refuted:           digest.count,
		RefutationDigest:  digest.finish(),
		Exhaustive:        true,
		Refutations:       samples,
	}, nil
}

// VerifyRefutationDigest independently replays the same exhaustive account
// over every legal projection with more categories than the optimum.
func VerifyRefutationDigest(protocol Protocol, config Config, certificate OptimalityCertificate) error {
	m, err := compileProtocol(protocol)
	if err != nil {
		return err
	}
	constraints, err := buildConstraints(m, config)
	if err != nil {
		return err
	}
	expected, err := buildOptimality(m, constraints, certificate.CategoryCount, certificate.CanonicalEncoding)
	if err != nil {
		return err
	}
	if expected.AboveBest != certificate.AboveBest ||
		expected.Refuted != certificate.Refuted ||
		expected.RefutationDigest != certificate.RefutationDigest ||
		expected.CanonicalEncoding != certificate.CanonicalEncoding ||
		expected.Exhaustive != certificate.Exhaustive {
		return fmt.Errorf("projection: optimality certificate does not match exhaustive replay")
	}
	return nil
}

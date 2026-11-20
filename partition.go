package bootpolicy

import (
	"math"
	"sort"
)

const (
	maxUint32 = math.MaxUint32
	maxUint64 = math.MaxUint64
)

// MaxPartitionCells bounds the finite enumeration performed while building an
// equivalence partition. Compression returns ErrPartitionTooLarge rather than
// silently sampling when a configuration exceeds it: equivalence must never be
// argued from sampled generations or bounded label depth.
var MaxPartitionCells = 8_000_000

// PrefixClass is one label-prefix equivalence region.
type PrefixClass struct {
	Representative  string
	IsNode          bool
	Prefix          string
	MatchedPrefixes []int
}

// IntervalClass is one generation equivalence region cut at range endpoints.
type IntervalClass struct {
	Representative uint32
	Lo             uint32
	Hi             uint32
	Tail           bool
}

// TimeClass is one boot-time region cut at exemption NotAfter boundaries.
type TimeClass struct {
	Representative uint64
	Lo             uint64
	Hi             uint64
	Tail           bool
}

// ValueClass is one exact-value region for signer IDs, locations or roots.
type ValueClass struct {
	Representative string
	Wildcard       bool
	MatchedIndexes []int
}

// DigestClass is one exact image-digest region.
type DigestClass struct {
	Representative Digest
	Wildcard       bool
	MatchedIndexes []int
}

// ParentClass enumerates distinct parent scopes.
type ParentClass struct {
	Scope           *ParentScope
	NilParent       bool
	Wildcard        bool
	MatchedIndexes  []int
	UnscopedIndexes []int
}

// Certificate is the equivalence-class certificate handed to the independent
// verifier, which enumerates the finite Cartesian product of the classes and
// recomputes both policies' decisions.
type Certificate struct {
	Prefixes           []string
	PrefixClasses      []PrefixClass
	GenClasses         []IntervalClass
	LocationClasses    []ValueClass
	DigestClasses      []DigestClass
	RootClasses        []ValueClass
	TimeClasses        []TimeClass
	ParentClasses      []ParentClass
	GroupDenyMasks     []uint64
	GroupExemptMasks   []uint64
	GroupOrigDenied    []bool
	OriginalEntryCount int
}

type prefixNode struct {
	prefix   string
	children []*prefixNode
}

type cell struct {
	event      LoadEvent
	denyMask   uint64
	exemptMask uint64
}

type cellGroup struct {
	denyMask   uint64
	exemptMask uint64
	origDenied bool
	event      LoadEvent
}

// Partition is the finite partition over all possible load events.
type Partition struct {
	entries []Entry
	cert    Certificate
	groups  []cellGroup
}

// Certificate returns the class certificate for the partition.
func (p *Partition) Certificate() Certificate {
	return p.cert
}

func decideDenied(denyMask, exemptMask uint64) bool {
	return denyMask != 0 && exemptMask == 0
}

func finishValueClasses(named []string, entries []Entry, pick func(*Entry) string) []ValueClass {
	sort.Strings(named)
	classes := make([]ValueClass, 0, len(named)+1)
	for _, v := range named {
		matched := make([]int, 0)
		for i := range entries {
			if pick(&entries[i]) == v {
				matched = append(matched, i)
			}
		}
		classes = append(classes, ValueClass{Representative: v, MatchedIndexes: matched})
	}
	classes = append(classes, ValueClass{Wildcard: true})
	return classes
}

func buildSignerClasses(entries []Entry) []ValueClass {
	seen := map[string]struct{}{}
	for i := range entries {
		seen[entries[i].SignerID] = struct{}{}
	}
	named := make([]string, 0, len(seen))
	for v := range seen {
		named = append(named, v)
	}
	return finishValueClasses(named, entries, func(e *Entry) string { return e.SignerID })
}

func buildLocationClasses(entries []Entry) []ValueClass {
	seen := map[string]struct{}{}
	for i := range entries {
		if entries[i].Location != "" {
			seen[entries[i].Location] = struct{}{}
		}
	}
	named := make([]string, 0, len(seen))
	for v := range seen {
		named = append(named, v)
	}
	return finishValueClasses(named, entries, func(e *Entry) string { return e.Location })
}

func buildRootClasses(entries []Entry) []ValueClass {
	seen := map[string]struct{}{}
	for i := range entries {
		if entries[i].Kind == KindExempt && entries[i].RootID != "" {
			seen[entries[i].RootID] = struct{}{}
		}
	}
	named := make([]string, 0, len(seen))
	for v := range seen {
		named = append(named, v)
	}
	sort.Strings(named)
	classes := make([]ValueClass, 0, len(named)+1)
	for _, v := range named {
		matched := make([]int, 0)
		for i := range entries {
			if entries[i].Kind == KindExempt && entries[i].RootID == v {
				matched = append(matched, i)
			}
		}
		classes = append(classes, ValueClass{Representative: v, MatchedIndexes: matched})
	}
	classes = append(classes, ValueClass{Wildcard: true})
	return classes
}

func buildDigestClasses(entries []Entry) []DigestClass {
	type key struct {
		alg string
		h   string
	}
	values := map[key]Digest{}
	for i := range entries {
		if entries[i].Kind == KindExempt {
			d := entries[i].Digest
			values[key{d.Algorithm, string(d.Hash)}] = d
		}
	}
	keys := make([]key, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].alg != keys[j].alg {
			return keys[i].alg < keys[j].alg
		}
		return keys[i].h < keys[j].h
	})
	classes := make([]DigestClass, 0, len(keys)+1)
	for _, k := range keys {
		d := values[k]
		matched := make([]int, 0)
		for i := range entries {
			if entries[i].Kind == KindExempt && entries[i].Digest.equal(d) {
				matched = append(matched, i)
			}
		}
		classes = append(classes, DigestClass{Representative: d, MatchedIndexes: matched})
	}
	classes = append(classes, DigestClass{Wildcard: true})
	return classes
}

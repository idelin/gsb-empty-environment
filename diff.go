package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one rectangle of operations the policy update newly opens:
// every point inside was effectively denied under the old policy and is
// explicitly allowed under the new one.
type Finding struct {
	// Rect is the value-based rectangle covered by this finding.
	Rect Rect
	// OldHits lists the old-policy rules firing on every point of Rect.
	OldHits []Hit
	// NewHits lists the new-policy rules firing on every point of Rect.
	NewHits []Hit
	// Points is the number of concrete domain tuples inside Rect.
	Points int
}

// Report is one shard of the semantic difference.
type Report struct {
	// Findings are pairwise disjoint rectangles.
	Findings []Finding
	// Remainder is the still-uncovered part of the target domain, itself a
	// disjoint rectangular cover. It is nil when Complete is true.
	Remainder []Rect
	// Complete reports whether every target point is covered by Findings.
	Complete bool
}

// Pager streams the non-overlapping cover in shards. The same target
// rectangles are emitted at most once across all Next calls.
type Pager struct {
	u        *Universe
	pending  []targetRect
	pageSize int
}

// Diff returns a Pager over the rectangles newly opened by the update.
// budget is the maximum number of findings returned by each Next call;
// a non-positive budget means one page covering the whole difference.
func Diff(u *Universe, oldP, newP Policy, budget int) (*Pager, error) {
	oldCompiled, err := compilePolicy(u, oldP)
	if err != nil {
		return nil, fmt.Errorf("netsec: old policy: %w", err)
	}
	newCompiled, err := compilePolicy(u, newP)
	if err != nil {
		return nil, fmt.Errorf("netsec: new policy: %w", err)
	}

	targets := targetCover(u, oldCompiled, newCompiled)
	if budget <= 0 {
		budget = len(targets)
		if budget == 0 {
			budget = 1
		}
	}
	return &Pager{u: u, pending: targets, pageSize: budget}, nil
}

// Next returns the next shard. The final shard has Complete set to true and a
// nil remainder.
func (p *Pager) Next() Report {
	if len(p.pending) == 0 {
		return Report{Findings: nil, Remainder: nil, Complete: true}
	}

	n := p.pageSize
	if n > len(p.pending) {
		n = len(p.pending)
	}
	page := p.pending[:n]
	p.pending = p.pending[n:]

	findings := make([]Finding, 0, n)
	for _, target := range page {
		findings = append(findings, Finding{
			Rect:    p.u.toNamed(target.rect),
			OldHits: append([]Hit(nil), target.oldHits...),
			NewHits: append([]Hit(nil), target.newHits...),
			Points:  target.rect.volume(),
		})
	}

	if len(p.pending) == 0 {
		return Report{Findings: findings, Remainder: nil, Complete: true}
	}
	remainder := make([]Rect, len(p.pending))
	for i, target := range p.pending {
		remainder[i] = p.u.toNamed(target.rect)
	}
	return Report{Findings: findings, Remainder: remainder, Complete: false}
}

// targetRect is one rectangle of target points with constant hit annotations.
type targetRect struct {
	rect    rect
	oldHits []Hit
	newHits []Hit
}

// targetCover computes the disjoint, coalesced cover of points that are
// denied under oldCompiled and explicitly allowed under newCompiled.
func targetCover(u *Universe, oldCompiled, newCompiled compiledPolicy) []targetRect {
	cells := []rect{fullRect(u)}
	for _, compiled := range oldCompiled.rules {
		cells = splitCells(cells, compiled.node.truth)
	}
	for _, compiled := range newCompiled.rules {
		cells = splitCells(cells, compiled.node.truth)
	}

	var targets []targetRect
	for _, cell := range cells {
		probe := representativePoint(cell)
		oldDecision := oldCompiled.decide(probe)
		newDecision := newCompiled.decide(probe)
		if !oldDecision.Allowed && newDecision.Allowed {
			targets = append(targets, targetRect{
				rect:    cell,
				oldHits: oldDecision.Hits,
				newHits: newDecision.Hits,
			})
		}
	}

	targets = coalesceTargets(targets)
	sort.Slice(targets, func(i, j int) bool {
		return rectLess(targets[i].rect, targets[j].rect)
	})
	return targets
}

// splitCells refines the disjoint cover so every boundary of cut becomes a
// cell boundary; output cells remain pairwise disjoint.
func splitCells(cells []rect, cuts []rect) []rect {
	var out []rect
	for _, cell := range cells {
		var overlapping []rect
		for _, cut := range cuts {
			if piece := intersect(cell, cut); !piece.empty() {
				overlapping = append(overlapping, piece)
			}
		}
		out = append(out, subtractMany(cell, overlapping)...)
		out = append(out, overlapping...)
	}
	return out
}

// representativePoint picks the minimum index of each axis set. Rule truth is
// constant inside any refined cell, so the choice of representative does not
// matter.
func representativePoint(r rect) []int {
	point := make([]int, len(r.sets))
	for dim, set := range r.sets {
		point[dim] = set[0]
		for _, v := range set[1:] {
			if v < point[dim] {
				point[dim] = v
			}
		}
	}
	return point
}

// coalesceTargets merges adjacent cells that carry identical hit annotations.
func coalesceTargets(targets []targetRect) []targetRect {
	if len(targets) <= 1 {
		return targets
	}
	groups := make(map[string][]rect)
	annotation := make(map[string]targetRect)
	for _, target := range targets {
		key := hitKey(target.oldHits, target.newHits)
		groups[key] = append(groups[key], target.rect)
		annotation[key] = target
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out []targetRect
	for _, key := range keys {
		// The grouping key itself keeps merges inside one annotation class,
		// so every merged rectangle keeps constant old/new hits.
		for _, r := range mergeRects(groups[key], func(rect) string { return key }) {
			info := annotation[key]
			out = append(out, targetRect{
				rect:    r,
				oldHits: info.oldHits,
				newHits: info.newHits,
			})
		}
	}
	return out
}

func hitKey(oldHits, newHits []Hit) string {
	var b strings.Builder
	b.WriteString("o:")
	for _, hit := range oldHits {
		b.WriteString(hit.RuleID)
		b.WriteByte('=')
		b.WriteString(string(hit.Effect))
		b.WriteByte(';')
	}
	b.WriteString("|n:")
	for _, hit := range newHits {
		b.WriteString(hit.RuleID)
		b.WriteByte('=')
		b.WriteString(string(hit.Effect))
		b.WriteByte(';')
	}
	return b.String()
}

func rectEqual(a, b rect) bool {
	if len(a.sets) != len(b.sets) {
		return false
	}
	for dim := range a.sets {
		if !equalSet(a.sets[dim], b.sets[dim]) {
			return false
		}
	}
	return true
}

func rectContained(inner, outer rect) bool {
	for dim := range inner.sets {
		for _, v := range inner.sets[dim] {
			if !containsInt(outer.sets[dim], v) {
				return false
			}
		}
	}
	return true
}

// rectLess gives a deterministic lexicographic ordering over rectangles.
func rectLess(a, b rect) bool {
	for dim := range a.sets {
		left := append([]int(nil), a.sets[dim]...)
		right := append([]int(nil), b.sets[dim]...)
		sort.Ints(left)
		sort.Ints(right)
		if cmp := sliceLess(left, right); cmp != 0 {
			return cmp < 0
		}
	}
	return false
}

func sliceLess(a, b []int) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

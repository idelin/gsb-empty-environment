package taskpkg

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	// MaxBlocks is the largest number of visible text blocks accepted by Order.
	MaxBlocks = 30
	// MaxDividers is the largest number of candidate column dividers accepted
	// by Order.
	MaxDividers = 6
)

var (
	ErrTooManyBlocks    = errors.New("reading order: too many blocks")
	ErrTooManyDividers  = errors.New("reading order: too many candidate dividers")
	ErrInvalidBlockID   = errors.New("reading order: block ID must not be empty")
	ErrDuplicateBlockID = errors.New("reading order: duplicate block ID")
	ErrInvalidRect      = errors.New("reading order: invalid rectangle")
	ErrInvalidDivider   = errors.New("reading order: invalid candidate divider")
	ErrInvalidDirection = errors.New("reading order: invalid writing direction")
	ErrAncestorCycle    = errors.New("reading order: ancestor cycle")
)

// Direction is the writing direction used to order columns.
type Direction string

const (
	// LTR orders columns from left to right.
	LTR Direction = "ltr"
	// RTL orders columns from right to left.
	RTL Direction = "rtl"

	// Long-form spellings are provided for callers that prefer descriptive
	// names.
	LeftToRight  Direction = LTR
	RightToLeft  Direction = RTL
	DirectionLTR Direction = LTR
	DirectionRTL Direction = RTL
)

// Rect is a text block's visible rectangle in CSS pixels. Width and Height
// must be non-negative. Rectangles are measured from the top-left corner, so Y
// grows downwards and X grows to the right in both writing directions.
type Rect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// NewRect constructs a rectangle from its top-left corner and size.
func NewRect(x, y, width, height float64) Rect {
	return Rect{X: x, Y: y, Width: width, Height: height}
}

// RectFromEdges constructs a rectangle from its left, top, right and bottom
// edges.
func RectFromEdges(left, top, right, bottom float64) Rect {
	return Rect{X: left, Y: top, Width: right - left, Height: bottom - top}
}

// Left returns the left edge of r.
func (r Rect) Left() float64 { return r.X }

// Top returns the top edge of r.
func (r Rect) Top() float64 { return r.Y }

// Right returns the right edge of r.
func (r Rect) Right() float64 { return r.X + r.Width }

// Bottom returns the bottom edge of r.
func (r Rect) Bottom() float64 { return r.Y + r.Height }

// CenterX returns the horizontal center of r.
func (r Rect) CenterX() float64 { return r.X + r.Width/2 }

// Block is one visible text block. Heading must be set explicitly by the
// extractor: geometry alone never promotes a block to a heading. Ancestors may
// contain IDs of visible blocks and/or non-text DOM containers; IDs not present
// in Input.Blocks are ignored for relative ordering.
type Block struct {
	ID        string
	Rect      Rect
	Ancestors []string
	Heading   bool
}

// Input is the complete payload needed to calculate reading order.
type Input struct {
	Blocks    []Block
	Dividers  []float64
	Direction Direction
}

// ConflictReason identifies why a block needed special handling.
type ConflictReason string

const (
	// ReasonCrossesDivider means a non-heading block intersects a selected
	// column divider. These are the only crossings counted by Result.Crossings.
	ReasonCrossesDivider ConflictReason = "non_heading_crosses_divider"
	// ReasonHeadingSpansDivider means an explicitly marked heading intersects a
	// selected divider. It is reported but is deliberately not penalized.
	ReasonHeadingSpansDivider ConflictReason = "heading_spans_divider"
	// ReasonAncestorColumn means a block's structural owner is in a different
	// geometric column, so the block follows its ancestor's column.
	ReasonAncestorColumn ConflictReason = "ancestor_column_override"
	// ReasonAncestorOrder means vertical order had to yield to an explicit
	// ancestor-before-descendant constraint.
	ReasonAncestorOrder ConflictReason = "ancestor_precedence"
	// ReasonConstraintCycle means a pathological combination of structure and
	// spanning headings could not be satisfied in one pass; structural edges
	// were retained and the remaining order fell back to the stable base order.
	ReasonConstraintCycle ConflictReason = "constraint_cycle"
)

// Conflict is one concrete ordering conflict. Divider is meaningful for the
// two divider-crossing reasons; AncestorID is meaningful for structural
// reasons. Detail is a human-readable explanation and is not used as an API
// discriminator.
type Conflict struct {
	BlockID    string
	Reason     ConflictReason
	Divider    float64
	AncestorID string
	Detail     string
}

func (c Conflict) String() string {
	if c.Detail != "" {
		return c.Detail
	}
	return fmt.Sprintf("%s: %s", c.BlockID, c.Reason)
}

// Result is a deterministic reading order and the conflict records produced
// while calculating it.
type Result struct {
	// Order contains every input block ID exactly once.
	Order []string
	// Dividers contains the selected candidate divider positions in ascending
	// order.
	Dividers []float64
	// Crossings is the number of distinct non-heading blocks crossed by the
	// selected dividers.
	Crossings int
	// Conflicts contains every divider crossing and every structural override
	// detected for the selected partition.
	Conflicts []Conflict
}

// Order calculates reading order from in.
func Order(in Input) (Result, error) {
	prepared, err := prepare(in)
	if err != nil {
		return Result{}, err
	}
	if len(prepared.items) == 0 {
		return Result{Order: []string{}}, nil
	}

	selected, crossings := choosePartition(prepared.items, prepared.dividers)
	prepared.selected = selected
	prepared.columnCount = len(selected) + 1

	prepared.assignStructuralColumns()
	conflicts := prepared.geometricConflicts()
	order, cycle := prepared.structuralOrder()
	conflicts = append(conflicts, prepared.structuralConflicts()...)
	if cycle {
		for _, id := range order {
			conflicts = append(conflicts, Conflict{
				BlockID: id,
				Reason:  ReasonConstraintCycle,
				Detail:  fmt.Sprintf("block %q was ordered using ancestor constraints only", id),
			})
		}
	}
	sortConflicts(conflicts)

	dividers := append([]float64(nil), selected...)
	return Result{
		Order:     order,
		Dividers:  dividers,
		Crossings: crossings,
		Conflicts: conflicts,
	}, nil
}

// ReadingOrder is an alias for Order.
func ReadingOrder(in Input) (Result, error) {
	return Order(in)
}

// OrderBlocks is a convenience form of Order for hosts that keep the three
// inputs separately.
func OrderBlocks(blocks []Block, dividers []float64, direction Direction) (Result, error) {
	return Order(Input{Blocks: blocks, Dividers: dividers, Direction: direction})
}

// OrderIDs returns only the ordered node IDs.
func OrderIDs(in Input) ([]string, error) {
	result, err := Order(in)
	if err != nil {
		return nil, err
	}
	return result.Order, nil
}

type box struct {
	left   float64
	top    float64
	right  float64
	bottom float64
}

func (b box) centerX() float64 { return (b.left + b.right) / 2 }

type item struct {
	id      string
	heading bool
	box     box

	geomColumn     int
	column         int
	topRoot        int
	columnOverride bool
}

type preparedInput struct {
	items       []item
	dividers    []float64
	selected    []float64
	columnCount int
	rtl         bool

	knownAncestor [][]bool
}

func prepare(in Input) (preparedInput, error) {
	if len(in.Blocks) > MaxBlocks {
		return preparedInput{}, fmt.Errorf("%w: got %d, maximum %d", ErrTooManyBlocks, len(in.Blocks), MaxBlocks)
	}
	direction := Direction(strings.ToLower(strings.TrimSpace(string(in.Direction))))
	if direction == "" {
		direction = LTR
	}
	if direction != LTR && direction != RTL {
		return preparedInput{}, fmt.Errorf("%w: %q", ErrInvalidDirection, in.Direction)
	}

	byID := make(map[string]int, len(in.Blocks))
	items := make([]item, len(in.Blocks))
	for i, block := range in.Blocks {
		if block.ID == "" {
			return preparedInput{}, fmt.Errorf("%w at index %d", ErrInvalidBlockID, i)
		}
		if _, exists := byID[block.ID]; exists {
			return preparedInput{}, fmt.Errorf("%w: %q", ErrDuplicateBlockID, block.ID)
		}
		byID[block.ID] = i

		r := block.Rect
		if !finite(r.X) || !finite(r.Y) || !finite(r.Width) || !finite(r.Height) ||
			r.Width < 0 || r.Height < 0 {
			return preparedInput{}, fmt.Errorf("%w for block %q", ErrInvalidRect, block.ID)
		}
		items[i] = item{
			id:      block.ID,
			heading: block.Heading,
			box: box{
				left:   r.X,
				top:    r.Y,
				right:  r.X + r.Width,
				bottom: r.Y + r.Height,
			},
		}
	}

	dividers := make([]float64, 0, len(in.Dividers))
	seenDivider := make(map[float64]struct{}, len(in.Dividers))
	for _, divider := range in.Dividers {
		if !finite(divider) {
			return preparedInput{}, fmt.Errorf("%w: %v", ErrInvalidDivider, divider)
		}
		if _, exists := seenDivider[divider]; exists {
			continue
		}
		seenDivider[divider] = struct{}{}
		dividers = append(dividers, divider)
	}
	sort.Float64s(dividers)
	if len(dividers) > MaxDividers {
		return preparedInput{}, fmt.Errorf("%w: got %d distinct lines, maximum %d", ErrTooManyDividers, len(dividers), MaxDividers)
	}

	known := make([][]bool, len(items))
	for i := range known {
		known[i] = make([]bool, len(items))
	}
	for i, block := range in.Blocks {
		seen := make(map[int]struct{}, len(block.Ancestors))
		for _, ancestorID := range block.Ancestors {
			ancestor, ok := byID[ancestorID]
			if !ok {
				continue
			}
			if ancestor == i {
				return preparedInput{}, fmt.Errorf("%w: block %q lists itself as an ancestor", ErrAncestorCycle, block.ID)
			}
			if _, exists := seen[ancestor]; exists {
				continue
			}
			seen[ancestor] = struct{}{}
			known[ancestor][i] = true
		}
	}

	if hasAncestorCycle(known) {
		return preparedInput{}, ErrAncestorCycle
	}
	closeAncestorRelations(known)

	// A structural owner decides the column for its whole visible subtree.
	// This is what keeps a floating annotation with its DOM section instead of
	// assigning it to whichever column happens to be nearest.
	rootMemo := make([]int, len(items))
	for i := range rootMemo {
		rootMemo[i] = -1
	}
	var findRoot func(int) int
	findRoot = func(i int) int {
		if rootMemo[i] >= 0 {
			return rootMemo[i]
		}
		root := i
		for ancestor := 0; ancestor < len(items); ancestor++ {
			if known[ancestor][i] {
				if root == i || items[ancestor].id < items[root].id {
					root = ancestor
				}
			}
		}
		if root == i {
			rootMemo[i] = i
		} else {
			rootMemo[i] = findRoot(root)
		}
		return rootMemo[i]
	}
	for i := range items {
		items[i].topRoot = findRoot(i)
	}

	return preparedInput{
		items:         items,
		dividers:      dividers,
		rtl:           direction == RTL,
		knownAncestor: known,
	}, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func hasAncestorCycle(known [][]bool) bool {
	const (
		unseen = iota
		visiting
		done
	)
	state := make([]int, len(known))
	var visit func(int) bool
	visit = func(node int) bool {
		switch state[node] {
		case visiting:
			return true
		case done:
			return false
		}
		state[node] = visiting
		for child := range known[node] {
			if known[node][child] && visit(child) {
				return true
			}
		}
		state[node] = done
		return false
	}
	for node := range known {
		if visit(node) {
			return true
		}
	}
	return false
}

func closeAncestorRelations(known [][]bool) {
	for via := range known {
		for from := range known {
			if !known[from][via] {
				continue
			}
			for to := range known {
				if known[via][to] {
					known[from][to] = true
				}
			}
		}
	}
}

func (p *preparedInput) assignStructuralColumns() {
	for i := range p.items {
		p.items[i].geomColumn = columnAt(p.items[i].box.centerX(), p.selected)
	}
	for i := range p.items {
		root := p.items[i].topRoot
		p.items[i].column = p.items[root].geomColumn
		p.items[i].columnOverride = p.items[i].column != p.items[i].geomColumn
	}
}

func columnAt(x float64, dividers []float64) int {
	return sort.Search(len(dividers), func(i int) bool {
		return dividers[i] >= x
	})
}

func crossesDivider(r box, divider float64) bool {
	return r.left < divider && divider < r.right
}

func sortConflicts(conflicts []Conflict) {
	sort.SliceStable(conflicts, func(i, j int) bool {
		a, b := conflicts[i], conflicts[j]
		if a.BlockID != b.BlockID {
			return a.BlockID < b.BlockID
		}
		if a.Reason != b.Reason {
			return a.Reason < b.Reason
		}
		if a.Divider != b.Divider {
			return a.Divider < b.Divider
		}
		return a.AncestorID < b.AncestorID
	})
}

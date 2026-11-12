package taskpkg

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const (
	// MaxBlocks is the largest number of visible text blocks accepted by
	// BuildReadingOrder.
	MaxBlocks = 30
	// MaxDividers is the largest number of candidate column dividers accepted
	// by BuildReadingOrder.
	MaxDividers = 6
)

var (
	ErrTooManyBlocks    = errors.New("reading order: too many text blocks")
	ErrTooManyDividers  = errors.New("reading order: too many candidate dividers")
	ErrInvalidRect      = errors.New("reading order: invalid rectangle")
	ErrInvalidDivider   = errors.New("reading order: invalid candidate divider")
	ErrDuplicateID      = errors.New("reading order: duplicate node ID")
	ErrInvalidDirection = errors.New("reading order: invalid writing direction")
)

// Direction is the inline writing direction of the page. It is a string alias
// so callers can pass the host's raw direction value directly.
type Direction = string

// WritingDirection is a descriptive alias for Direction.
type WritingDirection = Direction

const (
	LTR         Direction = "ltr"
	RTL         Direction = "rtl"
	LeftToRight           = LTR
	RightToLeft           = RTL
)

// Rect describes a text block's page rectangle. A caller may use either
// X/Y/Width/Height or Left/Top/Right/Bottom. X/Y/Width/Height takes precedence
// when Width or Height is non-zero.
type Rect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64

	Left   float64
	Top    float64
	Right  float64
	Bottom float64
}

// Block is one visible text block. ID (or NodeID, when set) is the original
// DOM node ID. Ancestors contains the IDs of its DOM ancestors in any order;
// it accepts []int, []string, []any, or a single ID. Only the explicit relation
// is used, never coordinate proximity. Heading blocks are explicitly marked
// and are exempt from divider-crossing cost.
type Block struct {
	ID     any
	NodeID any
	Rect   Rect

	Ancestors   any
	AncestorIDs any

	Heading   bool
	IsHeading bool

	// DOMIndex only makes duplicate/malformed input deterministic. The original
	// node ID remains the required public tie-breaker.
	DOMIndex int
}

// LayoutBlock is an alias for Block.
type LayoutBlock = Block

// Reason is a stable machine-readable explanation of an ordering conflict.
type Reason string

// ConflictReason is an alias for Reason.
type ConflictReason = Reason

const (
	// ReasonCrossesDivider marks a non-heading block intersected by a selected
	// divider. It contributes to the partition cost.
	ReasonCrossesDivider Reason = "nonheading-crosses-divider"
	// ReasonHeadingSpansDivider marks a heading shown across a selected
	// divider. It is reported but does not contribute to the partition cost.
	ReasonHeadingSpansDivider Reason = "heading-spans-divider"
	// ReasonAncestorCycle means the explicit ancestor graph contains a cycle.
	ReasonAncestorCycle Reason = "ancestor-cycle"
	// ReasonAncestorReordered means an explicit ancestor constraint overrode
	// the block's base vertical/column position.
	ReasonAncestorReordered Reason = "ancestor-reordered"
	// ReasonAncestorAcrossColumns means an ancestor and descendant were
	// assigned to different inferred columns.
	ReasonAncestorAcrossColumns Reason = "ancestor-crosses-column"
)

// Conflict lists every reason attached to one block. Blocks without a reason
// are omitted from Result.Conflicts.
type Conflict struct {
	NodeID  string
	Reasons []Reason
	// Dividers contains selected divider values relevant to this block.
	Dividers []float64
	// AncestorIDs contains explicit ancestor IDs relevant to this block.
	AncestorIDs []string
}

// Result is the reading order and the structural diagnostics used to produce
// it. Order and NodeIDs have the same length. IDs are canonical strings so both
// integer and string DOM node IDs produce stable results.
type Result struct {
	Order               []string
	NodeIDs             []any
	Conflicts           []Conflict
	SelectedDividers    []float64
	DividerIndices      []int
	UnusedDividers      []float64
	CrossingCount       int
	EvaluatedPartitions int
}

// ReadingOrder is an alias for Result.
type ReadingOrder = Result

type rectangle struct {
	left   float64
	top    float64
	right  float64
	bottom float64
}

type textBlock struct {
	id        string
	rawID     any
	index     int
	rect      rectangle
	heading   bool
	ancestors []string
	column    int
	crossed   []int
}

type divider struct {
	value         float64
	originalIndex int
}

type partition struct {
	indices       []int
	crossedBlocks int
}

// BuildReadingOrder computes a reading order for at most 30 visible text
// blocks and at most 6 candidate vertical dividers.
//
// Every non-empty subset of effective candidate dividers is enumerated. A
// divider is effective when at least one block lies wholly to its left and
// another lies wholly to its right. A partition is valid only when every column
// it creates contains at least one block. The primary cost is the number of
// distinct non-heading blocks intersected by a selected divider. Ties prefer
// more selected dividers, then the lexicographically earliest candidate
// indices.
//
// Columns are read left-to-right for LTR and right-to-left for RTL. A heading
// crossing one or more dividers is assigned to the first logical column it
// spans, placing a page-wide article heading before its body. Within a column,
// blocks are ordered by rectangle top and then original node ID. Explicit DOM
// ancestor edges are hard ordering constraints and are never inferred from
// nearby coordinates.
func BuildReadingOrder(blocks []Block, dividers []float64, direction Direction) (Result, error) {
	if len(blocks) > MaxBlocks {
		return Result{}, fmt.Errorf("%w: got %d, limit %d", ErrTooManyBlocks, len(blocks), MaxBlocks)
	}
	if len(dividers) > MaxDividers {
		return Result{}, fmt.Errorf("%w: got %d, limit %d", ErrTooManyDividers, len(dividers), MaxDividers)
	}

	normalizedDirection, err := normalizeDirection(direction)
	if err != nil {
		return Result{}, err
	}

	normalizedBlocks, err := normalizeBlocks(blocks)
	if err != nil {
		return Result{}, err
	}

	normalizedDividers, err := normalizeDividers(dividers)
	if err != nil {
		return Result{}, err
	}

	effective := make([]int, 0, len(normalizedDividers))
	for index := range normalizedDividers {
		if dividerIsEffective(normalizedBlocks, normalizedDividers[index].value) {
			effective = append(effective, index)
		}
	}

	validPartitions, evaluatedPartitions := enumeratePartitions(
		normalizedBlocks,
		normalizedDividers,
		effective,
	)
	chosen := choosePartition(validPartitions)

	selectedValues := make([]float64, len(chosen.indices))
	selectedOriginalIndices := make([]int, len(chosen.indices))
	for index, dividerIndex := range chosen.indices {
		selectedValues[index] = normalizedDividers[dividerIndex].value
		selectedOriginalIndices[index] = normalizedDividers[dividerIndex].originalIndex
	}

	assignLayout(normalizedBlocks, chosen.indices, selectedValues, normalizedDirection)

	conflicts := make(map[int]*Conflict)
	addConflict := func(blockIndex int, reason Reason) {
		conflict, ok := conflicts[blockIndex]
		if !ok {
			conflict = &Conflict{NodeID: normalizedBlocks[blockIndex].id}
			conflicts[blockIndex] = conflict
		}
		if !containsReason(conflict.Reasons, reason) {
			conflict.Reasons = append(conflict.Reasons, reason)
		}
	}
	addDetail := func(blockIndex int, dividerValue *float64, ancestorID string) {
		conflict := conflicts[blockIndex]
		if conflict == nil {
			return
		}
		if dividerValue != nil {
			conflict.Dividers = appendUniqueFloat(conflict.Dividers, *dividerValue)
		}
		if ancestorID != "" && !containsString(conflict.AncestorIDs, ancestorID) {
			conflict.AncestorIDs = append(conflict.AncestorIDs, ancestorID)
		}
	}

	for blockIndex := range normalizedBlocks {
		block := &normalizedBlocks[blockIndex]
		for _, dividerIndex := range block.crossed {
			if block.heading {
				addConflict(blockIndex, ReasonHeadingSpansDivider)
			} else {
				addConflict(blockIndex, ReasonCrossesDivider)
			}
			value := normalizedDividers[dividerIndex].value
			addDetail(blockIndex, &value, "")
		}
	}

	ancestorEdges := buildAncestorEdges(normalizedBlocks)
	for blockIndex := range normalizedBlocks {
		block := &normalizedBlocks[blockIndex]
		for ancestorIndex, ancestorID := range block.ancestors {
			if ancestorEdges[blockIndex][ancestorIndex] < 0 {
				// A DOM ancestor often identifies a wrapper element rather
				// than another visible text block. Such nodes impose no
				// block-to-block precedence and are not conflicts.
				continue
			}
			parent := ancestorEdges[blockIndex][ancestorIndex]
			if block.column != normalizedBlocks[parent].column {
				addConflict(blockIndex, ReasonAncestorAcrossColumns)
				addDetail(blockIndex, nil, ancestorID)
			}
		}
	}

	baseOrder := makeBaseOrder(normalizedBlocks, normalizedDirection)
	baseRank := rankOrder(baseOrder)
	for child, parents := range ancestorEdges {
		for _, parent := range parents {
			if parent >= 0 && baseRank[child] < baseRank[parent] {
				addConflict(child, ReasonAncestorReordered)
				addDetail(child, nil, normalizedBlocks[parent].id)
			}
		}
	}

	cycleNodes := findAncestorCycles(ancestorEdges)
	for _, blockIndex := range cycleNodes {
		addConflict(blockIndex, ReasonAncestorCycle)
	}

	ordered := topoOrder(normalizedBlocks, ancestorEdges, baseRank, baseOrder)

	unused := make([]float64, 0, len(normalizedDividers))
	for index := range normalizedDividers {
		if !containsInt(chosen.indices, index) {
			unused = append(unused, normalizedDividers[index].value)
		}
	}

	result := Result{
		Order:               make([]string, 0, len(normalizedBlocks)),
		NodeIDs:             make([]any, 0, len(normalizedBlocks)),
		SelectedDividers:    selectedValues,
		DividerIndices:      selectedOriginalIndices,
		UnusedDividers:      unused,
		CrossingCount:       chosen.crossedBlocks,
		EvaluatedPartitions: evaluatedPartitions,
	}
	for _, blockIndex := range ordered {
		result.Order = append(result.Order, normalizedBlocks[blockIndex].id)
		result.NodeIDs = append(result.NodeIDs, normalizedBlocks[blockIndex].rawID)
	}
	for _, blockIndex := range ordered {
		if conflict := conflicts[blockIndex]; conflict != nil {
			result.Conflicts = append(result.Conflicts, *conflict)
		}
	}

	return result, nil
}

// Build is a convenience alias for BuildReadingOrder.
func Build(blocks []Block, dividers []float64, direction Direction) (Result, error) {
	return BuildReadingOrder(blocks, dividers, direction)
}

// OrderBlocks is a convenience alias for BuildReadingOrder.
func OrderBlocks(blocks []Block, dividers []float64, direction Direction) (Result, error) {
	return BuildReadingOrder(blocks, dividers, direction)
}

// ComputeReadingOrder is a convenience alias for BuildReadingOrder.
func ComputeReadingOrder(blocks []Block, dividers []float64, direction Direction) (Result, error) {
	return BuildReadingOrder(blocks, dividers, direction)
}

func normalizeDirection(direction Direction) (Direction, error) {
	switch strings.ToLower(strings.TrimSpace(string(direction))) {
	case "", "ltr", "left-to-right", "lefttoright", "horizontal-tb":
		return LTR, nil
	case "rtl", "right-to-left", "righttoleft":
		return RTL, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidDirection, direction)
	}
}

func normalizeBlocks(blocks []Block) ([]textBlock, error) {
	normalized := make([]textBlock, 0, len(blocks))
	ids := make(map[string]int, len(blocks))

	for index, block := range blocks {
		rawID := block.NodeID
		if rawID == nil {
			rawID = block.ID
		}
		if rawID == nil {
			rawID = index
		}
		id := canonicalNodeID(rawID)
		if _, exists := ids[id]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateID, id)
		}
		ids[id] = index

		rect, err := normalizeRect(block.Rect)
		if err != nil {
			return nil, fmt.Errorf("%w: block %q: %v", ErrInvalidRect, id, err)
		}

		ancestorValues, err := flattenAncestorIDs(block.Ancestors)
		if err != nil {
			return nil, fmt.Errorf("reading order: block %q: %w", id, err)
		}
		if len(ancestorValues) == 0 {
			ancestorValues, err = flattenAncestorIDs(block.AncestorIDs)
			if err != nil {
				return nil, fmt.Errorf("reading order: block %q: %w", id, err)
			}
		}
		ancestors := make([]string, 0, len(ancestorValues))
		seen := make(map[string]struct{}, len(ancestorValues))
		for _, ancestor := range ancestorValues {
			if ancestor == nil {
				continue
			}
			ancestorID := canonicalNodeID(ancestor)
			if _, exists := seen[ancestorID]; exists {
				continue
			}
			seen[ancestorID] = struct{}{}
			ancestors = append(ancestors, ancestorID)
		}

		normalized = append(normalized, textBlock{
			id:        id,
			rawID:     rawID,
			index:     index,
			rect:      rect,
			heading:   block.Heading || block.IsHeading,
			ancestors: ancestors,
		})
	}

	return normalized, nil
}

func flattenAncestorIDs(value any) ([]any, error) {
	if value == nil {
		return nil, nil
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		values := make([]any, 0, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			values = append(values, reflected.Index(index).Interface())
		}
		return values, nil
	case reflect.Pointer, reflect.Interface:
		if reflected.IsNil() {
			return nil, nil
		}
		return flattenAncestorIDs(reflected.Elem().Interface())
	default:
		return []any{value}, nil
	}
}

func normalizeRect(rect Rect) (rectangle, error) {
	values := []float64{
		rect.X, rect.Y, rect.Width, rect.Height,
		rect.Left, rect.Top, rect.Right, rect.Bottom,
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return rectangle{}, errors.New("coordinate is not finite")
		}
	}

	if rect.Width != 0 || rect.Height != 0 {
		if rect.Width < 0 || rect.Height < 0 {
			return rectangle{}, errors.New("negative width or height")
		}
		return rectangle{
			left:   rect.X,
			top:    rect.Y,
			right:  rect.X + rect.Width,
			bottom: rect.Y + rect.Height,
		}, nil
	}

	if rect.Right < rect.Left || rect.Bottom < rect.Top {
		return rectangle{}, errors.New("right precedes left or bottom precedes top")
	}
	return rectangle{
		left:   rect.Left,
		top:    rect.Top,
		right:  rect.Right,
		bottom: rect.Bottom,
	}, nil
}

func normalizeDividers(dividers []float64) ([]divider, error) {
	normalized := make([]divider, 0, len(dividers))
	for index, value := range dividers {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("%w: divider %d is not finite", ErrInvalidDivider, index)
		}
		normalized = append(normalized, divider{value: value, originalIndex: index})
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].value == normalized[j].value {
			return normalized[i].originalIndex < normalized[j].originalIndex
		}
		return normalized[i].value < normalized[j].value
	})

	deduplicated := normalized[:0]
	for _, current := range normalized {
		if len(deduplicated) > 0 && deduplicated[len(deduplicated)-1].value == current.value {
			continue
		}
		deduplicated = append(deduplicated, current)
	}
	return deduplicated, nil
}

func dividerIsEffective(blocks []textBlock, value float64) bool {
	hasLeft := false
	hasRight := false
	for _, block := range blocks {
		if block.rect.right <= value {
			hasLeft = true
		}
		if block.rect.left >= value {
			hasRight = true
		}
	}
	return hasLeft && hasRight
}

func enumeratePartitions(
	blocks []textBlock,
	dividers []divider,
	effective []int,
) ([]partition, int) {
	evaluated := 1
	if len(effective) > 0 {
		evaluated = (1 << len(effective)) - 1
	}

	valid := make([]partition, 0, evaluated)
	for mask := 1; mask < 1<<len(effective); mask++ {
		indices := make([]int, 0, len(effective))
		for bit, dividerIndex := range effective {
			if mask&(1<<bit) != 0 {
				indices = append(indices, dividerIndex)
			}
		}
		if candidate, ok := scorePartition(blocks, dividers, indices); ok {
			valid = append(valid, candidate)
		}
	}

	if len(valid) == 0 {
		valid = append(valid, partition{})
	}
	return valid, evaluated
}

func scorePartition(blocks []textBlock, dividers []divider, indices []int) (partition, bool) {
	if len(indices) == 0 {
		return partition{}, true
	}

	values := dividerValues(dividers, indices)
	populated := make([]bool, len(values)+1)
	crossed := make([]bool, len(blocks))

	for blockIndex := range blocks {
		column := columnAtPoint(blockCenter(blocks[blockIndex].rect), values)
		if column < 0 || column >= len(populated) {
			return partition{}, false
		}
		populated[column] = true
		if blocks[blockIndex].heading {
			continue
		}
		for _, dividerValue := range values {
			if crossesX(blocks[blockIndex].rect, dividerValue) {
				crossed[blockIndex] = true
				break
			}
		}
	}

	for _, hasBlocks := range populated {
		if !hasBlocks {
			return partition{}, false
		}
	}

	crossingCount := 0
	for _, didCross := range crossed {
		if didCross {
			crossingCount++
		}
	}
	return partition{
		indices:       append([]int(nil), indices...),
		crossedBlocks: crossingCount,
	}, true
}

func choosePartition(partitions []partition) partition {
	chosen := partitions[0]
	for _, candidate := range partitions[1:] {
		switch {
		case candidate.crossedBlocks < chosen.crossedBlocks:
			chosen = candidate
		case candidate.crossedBlocks > chosen.crossedBlocks:
			continue
		case len(candidate.indices) > len(chosen.indices):
			chosen = candidate
		case len(candidate.indices) < len(chosen.indices):
			continue
		default:
			for index := range candidate.indices {
				if candidate.indices[index] == chosen.indices[index] {
					continue
				}
				if candidate.indices[index] < chosen.indices[index] {
					chosen = candidate
				}
				break
			}
		}
	}
	return chosen
}

func dividerValues(dividers []divider, indices []int) []float64 {
	values := make([]float64, len(indices))
	for index, dividerIndex := range indices {
		values[index] = dividers[dividerIndex].value
	}
	return values
}

func assignLayout(
	blocks []textBlock,
	selected []int,
	values []float64,
	direction Direction,
) {
	for blockIndex := range blocks {
		block := &blocks[blockIndex]
		block.column = columnAtPoint(blockCenter(block.rect), values)
		block.crossed = nil
		for dividerIndex := range selected {
			if crossesX(block.rect, values[dividerIndex]) {
				block.crossed = append(block.crossed, selected[dividerIndex])
			}
		}
	}

	for blockIndex := range blocks {
		block := &blocks[blockIndex]
		if !block.heading || len(block.crossed) == 0 {
			continue
		}
		leftColumn := columnAtPoint(block.rect.left, values)
		rightColumn := columnAtPoint(block.rect.right, values)
		if direction == RTL {
			block.column = rightColumn
		} else {
			block.column = leftColumn
		}
	}
}

func buildAncestorEdges(blocks []textBlock) [][]int {
	idToIndex := make(map[string]int, len(blocks))
	for index := range blocks {
		idToIndex[blocks[index].id] = index
	}

	edges := make([][]int, len(blocks))
	for blockIndex := range blocks {
		edges[blockIndex] = make([]int, len(blocks[blockIndex].ancestors))
		for ancestorIndex, ancestorID := range blocks[blockIndex].ancestors {
			parent, ok := idToIndex[ancestorID]
			if !ok {
				edges[blockIndex][ancestorIndex] = -1
				continue
			}
			edges[blockIndex][ancestorIndex] = parent
		}
	}
	return edges
}

func makeBaseOrder(blocks []textBlock, direction Direction) []int {
	order := make([]int, len(blocks))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		return baseBlockLess(blocks[order[i]], blocks[order[j]], direction)
	})
	return order
}

func baseBlockLess(left, right textBlock, direction Direction) bool {
	if left.column != right.column {
		if direction == RTL {
			return left.column > right.column
		}
		return left.column < right.column
	}
	if left.rect.top != right.rect.top {
		return left.rect.top < right.rect.top
	}
	if comparison := compareCanonicalIDs(left.id, right.id); comparison != 0 {
		return comparison < 0
	}
	return left.index < right.index
}

func rankOrder(order []int) []int {
	rank := make([]int, len(order))
	for position, blockIndex := range order {
		rank[blockIndex] = position
	}
	return rank
}

func topoOrder(
	blocks []textBlock,
	ancestorEdges [][]int,
	baseRank []int,
	baseOrder []int,
) []int {
	indegree := make([]int, len(blocks))
	children := make([][]int, len(blocks))
	for child, parents := range ancestorEdges {
		for _, parent := range parents {
			if parent < 0 {
				continue
			}
			children[parent] = append(children[parent], child)
			indegree[child]++
		}
	}

	ordered := make([]int, 0, len(blocks))
	emitted := make([]bool, len(blocks))
	for len(ordered) < len(blocks) {
		best := -1
		for blockIndex := range blocks {
			if emitted[blockIndex] || indegree[blockIndex] != 0 {
				continue
			}
			if best < 0 || baseRank[blockIndex] < baseRank[best] {
				best = blockIndex
			}
		}

		if best < 0 {
			// A cycle cannot be topologically sorted. Break it at the block
			// appearing first in the structural base order, then continue.
			for _, blockIndex := range baseOrder {
				if !emitted[blockIndex] {
					best = blockIndex
					break
				}
			}
		}

		emitted[best] = true
		ordered = append(ordered, best)
		for _, child := range children[best] {
			if !emitted[child] && indegree[child] > 0 {
				indegree[child]--
			}
		}
	}

	return ordered
}

func findAncestorCycles(ancestorEdges [][]int) []int {
	children := make([][]int, len(ancestorEdges))
	for child, parents := range ancestorEdges {
		for _, parent := range parents {
			if parent >= 0 {
				children[parent] = append(children[parent], child)
			}
		}
	}

	const (
		unseen = iota
		visiting
		done
	)
	state := make([]int, len(ancestorEdges))
	inCycle := make([]bool, len(ancestorEdges))
	path := make([]int, 0, len(ancestorEdges))
	pathPosition := make([]int, len(ancestorEdges))

	var visit func(int)
	visit = func(blockIndex int) {
		state[blockIndex] = visiting
		pathPosition[blockIndex] = len(path)
		path = append(path, blockIndex)

		for _, child := range children[blockIndex] {
			switch state[child] {
			case unseen:
				visit(child)
			case visiting:
				for position := pathPosition[child]; position < len(path); position++ {
					inCycle[path[position]] = true
				}
			}
		}

		path = path[:len(path)-1]
		state[blockIndex] = done
	}

	for blockIndex := range ancestorEdges {
		if state[blockIndex] == unseen {
			visit(blockIndex)
		}
	}

	cycles := make([]int, 0)
	for blockIndex, isCycle := range inCycle {
		if isCycle {
			cycles = append(cycles, blockIndex)
		}
	}
	return cycles
}

func columnAtPoint(point float64, dividers []float64) int {
	return sort.Search(len(dividers), func(index int) bool {
		return dividers[index] >= point
	})
}

func blockCenter(rect rectangle) float64 {
	return (rect.left + rect.right) / 2
}

func crossesX(rect rectangle, divider float64) bool {
	return rect.left < divider && divider < rect.right
}

func canonicalNodeID(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case int:
		return strconv.Itoa(typed)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func compareCanonicalIDs(left, right string) int {
	leftInt, leftErr := strconv.ParseInt(left, 10, 64)
	rightInt, rightErr := strconv.ParseInt(right, 10, 64)
	if leftErr == nil && rightErr == nil {
		switch {
		case leftInt < rightInt:
			return -1
		case leftInt > rightInt:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(left, right)
}

func containsReason(reasons []Reason, target Reason) bool {
	for _, reason := range reasons {
		if reason == target {
			return true
		}
	}
	return false
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUniqueFloat(values []float64, target float64) []float64 {
	for _, value := range values {
		if value == target {
			return values
		}
	}
	return append(values, target)
}

package taskpkg

import (
	"errors"
)

// 约束来自任务说明：小规模页面使用穷举，不引入近似或启发式搜索。
const (
	MaxBlocks         = 30
	MaxCandidateLines = 6
)

var (
	ErrTooManyBlocks    = errors.New("taskpkg: at most 30 visible text blocks are supported")
	ErrTooManyLines     = errors.New("taskpkg: at most 6 candidate lines are supported")
	ErrEmptyBlockID     = errors.New("taskpkg: block id must be the original node id and cannot be empty")
	ErrDuplicateBlockID = errors.New("taskpkg: duplicate block id")
	ErrDuplicateLineID  = errors.New("taskpkg: duplicate candidate line id")
	ErrInvalidRect      = errors.New("taskpkg: block rectangle must have finite non-negative width and height")
	ErrInvalidDirection = errors.New("taskpkg: writing direction must be ltr or rtl")
	ErrInvalidParent    = errors.New("taskpkg: parent relation contains a cycle")
)

// Direction is the inline / column progression direction.
type Direction string

// WritingDirection is the domain-language alias for Direction.
type WritingDirection = Direction

const (
	DirectionLTR Direction = "ltr"
	DirectionRTL Direction = "rtl"

	// Short aliases for hosts that prefer the abbreviated names.
	LTR = DirectionLTR
	RTL = DirectionRTL

	LeftToRight = DirectionLTR
	RightToLeft = DirectionRTL
)

// Rect is a CSS/DOMRect-like axis-aligned rectangle. X and Y are the top-left
// corner; coordinates may be floating-point CSS pixels.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// RectFromLTRB constructs a rectangle from left/top/right/bottom coordinates.
func RectFromLTRB(left, top, right, bottom float64) Rect {
	return Rect{X: left, Y: top, Width: right - left, Height: bottom - top}
}

func (r Rect) Left() float64   { return r.X }
func (r Rect) Top() float64    { return r.Y }
func (r Rect) Right() float64  { return r.X + r.Width }
func (r Rect) Bottom() float64 { return r.Y + r.Height }
func (r Rect) CenterX() float64 {
	return r.X + r.Width/2
}

func (r Rect) CenterY() float64 {
	return r.Y + r.Height/2
}

// Block is one visible text block. ID must remain stable for result references.
// ID is the final tie-breaker unless NodeID supplies the original node id.
// Ancestors are ordered from the outermost ancestor to the immediate parent;
// including the block ID at the end is also accepted.
type Block struct {
	ID string `json:"id"`

	// NodeID is an optional explicit original node identifier. When set it is
	// used for the final tie-break instead of ID. Most hosts can leave it empty
	// and put the stable node id in ID.
	NodeID string `json:"node_id,omitempty"`

	Rect Rect `json:"rect"`

	// Ancestors is the root-to-parent ancestor path.
	Ancestors []string `json:"ancestors,omitempty"`

	// AncestorIDs is a descriptive alias for Ancestors.
	AncestorIDs []string `json:"ancestor_ids,omitempty"`

	// DOMOrder is an optional positive document-order rank. Smaller means
	// earlier. Zero means unavailable.
	DOMOrder int `json:"dom_order,omitempty"`

	// Heading marks an explicit title/heading block. A selected line may cross
	// it without increasing the partition penalty.
	Heading bool `json:"heading,omitempty"`

	// Common explicit marker aliases. Heading status is never inferred from
	// text, font size, or geometry.
	IsHeading bool `json:"is_heading,omitempty"`
	Title     bool `json:"title,omitempty"`
	IsTitle   bool `json:"is_title,omitempty"`
}

func (b Block) nodeID() string {
	if b.NodeID != "" {
		return b.NodeID
	}
	return b.ID
}

// CandidateLine is a vertical column divider candidate.
type CandidateLine struct {
	ID string  `json:"id,omitempty"`
	X  float64 `json:"x"`
}

// Line is a short alias used by some Go hosts.
type Line = CandidateLine
type Divider = CandidateLine

// Request is the input to Plan.
type Request struct {
	Blocks           []Block             `json:"blocks"`
	CandidateLines   []CandidateLine     `json:"candidate_lines,omitempty"`
	Lines            []CandidateLine     `json:"lines,omitempty"`
	Dividers         []CandidateLine     `json:"dividers,omitempty"`
	Direction        Direction           `json:"direction,omitempty"`
	WritingDirection Direction           `json:"writing_direction,omitempty"`
	WritingDir       Direction           `json:"-"`
	Parents          map[string]string   `json:"parents,omitempty"`
	Parent           map[string]string   `json:"parent,omitempty"`
	AncestorPaths    map[string][]string `json:"ancestor_paths,omitempty"`
	Ancestors        map[string][]string `json:"ancestors,omitempty"`
	NodeOrder        map[string]int      `json:"node_order,omitempty"`
}

// Options is a compatibility name for callers that prefer Plan(Options).
type Options = Request

// Severity classifies a returned structural reason.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
)

// Conflict is one machine-readable structural reason. Code is stable; Reason
// is a concise Chinese explanation suitable for debugging or audit output.
type Conflict struct {
	Code         string   `json:"code"`
	Severity     Severity `json:"severity"`
	Reason       string   `json:"reason"`
	BlockID      string   `json:"block_id,omitempty"`
	OtherBlockID string   `json:"other_block_id,omitempty"`
	LineID       string   `json:"line_id,omitempty"`
	X            float64  `json:"x,omitempty"`
}

// Column is a visual column ordered from left to right. IDs are sorted within
// the column but exclude blocks that span a selected divider.
type Column struct {
	Index int      `json:"index"`
	IDs   []string `json:"ids"`
}

// Partition is the exhaustive evaluation of one candidate-line subset.
type Partition struct {
	Mask                   int      `json:"mask"`
	SelectedLineIDs        []string `json:"selected_line_ids"`
	CrossedBlocks          []string `json:"crossed_blocks"`
	CrossedNonHeadingCount int      `json:"crossed_non_heading_count"`
	CrossedHeadingCount    int      `json:"crossed_heading_count"`
	CrossingCount          int      `json:"crossing_count"`
	Optimal                bool     `json:"optimal"`
}

// Result contains the reading order and all audit reasons.
type Result struct {
	// Order is the complete flat reading order and contains every input ID once.
	Order []string `json:"order"`

	// Columns are visual left-to-right columns containing non-spanning blocks.
	Columns []Column `json:"columns"`

	// SelectedLineIDs is the deterministic best candidate-line subset.
	SelectedLineIDs []string `json:"selected_line_ids"`
	SelectedMask    int      `json:"selected_mask"`

	// Partitions contains the full 2^n enumeration, with Optimal marks.
	Partitions []Partition `json:"partitions"`

	CrossedBlocks           []string `json:"crossed_blocks"`
	CrossedNonHeadingBlocks []string `json:"crossed_non_heading_blocks"`
	CrossedHeadingBlocks    []string `json:"crossed_heading_blocks"`

	Conflicts []Conflict `json:"conflicts"`

	// Reasons is the human-readable view of Conflicts.
	Reasons []string `json:"reasons"`
}

// ReasonStrings returns conflict explanations as strings.
func (r *Result) ReasonStrings() []string {
	out := make([]string, len(r.Conflicts))
	for i, c := range r.Conflicts {
		out[i] = c.Reason
	}
	return out
}

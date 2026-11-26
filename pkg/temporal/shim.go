// Code generated compatibility shim re-exporting the canonical package at
// prescreen.local/ps-db7888bb15. It exists so the package is importable under common path/name
// conventions; all semantics live in the root package.
package temporal

import tk "prescreen.local/ps-db7888bb15"

// Domain kind constants.
const (
	Continuous = tk.Continuous
	Discrete   = tk.Discrete
)

// Sentinel errors.
var (
	ErrKindMismatch = tk.ErrKindMismatch
	ErrOverlap      = tk.ErrOverlap
	ErrEmptyRange   = tk.ErrEmptyRange
	ErrCoverage     = tk.ErrCoverage
	ErrUnknownTable = tk.ErrUnknownTable
	ErrNotFound     = tk.ErrNotFound
	ErrTxDone       = tk.ErrTxDone
)

// Type re-exports.
type (
	Constraint              = tk.Constraint
	CoverageConstraint      = tk.CoverageConstraint
	CoverageViolation       = tk.CoverageViolation
	DB                      = tk.DB
	Database                = tk.Database
	EmptyRangeError         = tk.EmptyRangeError
	Engine                  = tk.Engine
	ExcludeConstraint       = tk.ExcludeConstraint
	ExclusionConstraint     = tk.ExclusionConstraint
	ExclusionConstraintRule = tk.ExclusionConstraintRule
	ExclusionViolation      = tk.ExclusionViolation
	Executor                = tk.Executor
	Fields                  = tk.Fields
	ForeignKeyConstraint    = tk.ForeignKeyConstraint
	ForeignKeyCoverage      = tk.ForeignKeyCoverage
	IntRange                = tk.IntRange
	IntegerRange            = tk.IntegerRange
	Interval                = tk.Interval
	Key                     = tk.Key
	Kind                    = tk.Kind
	Period                  = tk.Period
	PortionReport           = tk.PortionReport
	Range                   = tk.Range
	RangeExecutor           = tk.RangeExecutor
	RangeTable              = tk.RangeTable
	Relation                = tk.Relation
	Row                     = tk.Row
	RowRef                  = tk.RowRef
	Schema                  = tk.Schema
	Span                    = tk.Span
	Store                   = tk.Store
	Table                   = tk.Table
	TemporalRange           = tk.TemporalRange
	TemporalTable           = tk.TemporalTable
	TimeRange               = tk.TimeRange
	TimestampRange          = tk.TimestampRange
	TsInterval              = tk.TsInterval
	Tx                      = tk.Tx
	Update                  = tk.Update
	ValidationError         = tk.ValidationError
	Validity                = tk.Validity
)

// Function re-exports.
var (
	Abuts                        = tk.Abuts
	AddExclusion                 = tk.AddExclusion
	AddForeignKey                = tk.AddForeignKey
	AddTable                     = tk.AddTable
	Adjacent                     = tk.Adjacent
	AllRange                     = tk.AllRange
	AsTime                       = tk.AsTime
	CheckConstraints             = tk.CheckConstraints
	CheckCoverage                = tk.CheckCoverage
	CheckCoverageConstraint      = tk.CheckCoverageConstraint
	CheckEmptyRanges             = tk.CheckEmptyRanges
	CheckExclusion               = tk.CheckExclusion
	CheckExclusionConstraint     = tk.CheckExclusionConstraint
	CheckForeignKey              = tk.CheckForeignKey
	CheckForeignKeyCoverage      = tk.CheckForeignKeyCoverage
	CheckTemporalCoverage        = tk.CheckTemporalCoverage
	CoerceKey                    = tk.CoerceKey
	CoerceRange                  = tk.CoerceRange
	CoveredBy                    = tk.CoveredBy
	CreateTable                  = tk.CreateTable
	Default                      = tk.Default
	Diff                         = tk.Diff
	Difference                   = tk.Difference
	Empty                        = tk.Empty
	Equal                        = tk.Equal
	HasOverlap                   = tk.HasOverlap
	IntInterval                  = tk.IntInterval
	IntPoint                     = tk.IntPoint
	Intersect                    = tk.Intersect
	IntersectE                   = tk.IntersectE
	Intersection                 = tk.Intersection
	IntersectionE                = tk.IntersectionE
	Intersects                   = tk.Intersects
	IsAdjacent                   = tk.IsAdjacent
	IsAdjacentRanges             = tk.IsAdjacentRanges
	IsCoveredBy                  = tk.IsCoveredBy
	KeyMatches                   = tk.KeyMatches
	KeyOf                        = tk.KeyOf
	KeysEqual                    = tk.KeysEqual
	LowerOpen                    = tk.LowerOpen
	Merge                        = tk.Merge
	MergeAdjacentRows            = tk.MergeAdjacentRows
	Micro                        = tk.Micro
	MissingCoverage              = tk.MissingCoverage
	MustParse                    = tk.MustParse
	MustParseIntRange            = tk.MustParseIntRange
	MustParsePeriod              = tk.MustParsePeriod
	MustParseRange               = tk.MustParseRange
	MustParseTimeRange           = tk.MustParseTimeRange
	NewBoundRange                = tk.NewBoundRange
	NewClosed                    = tk.NewClosed
	NewClosedOpen                = tk.NewClosedOpen
	NewDB                        = tk.NewDB
	NewDatabase                  = tk.NewDatabase
	NewDiscreteRange             = tk.NewDiscreteRange
	NewEngine                    = tk.NewEngine
	NewExclusionConstraint       = tk.NewExclusionConstraint
	NewExecutor                  = tk.NewExecutor
	NewFKCoverage                = tk.NewFKCoverage
	NewHalfOpen                  = tk.NewHalfOpen
	NewIntRange                  = tk.NewIntRange
	NewOpen                      = tk.NewOpen
	NewOpenClosed                = tk.NewOpenClosed
	NewPeriod                    = tk.NewPeriod
	NewRange                     = tk.NewRange
	NewRangeTable                = tk.NewRangeTable
	NewSchema                    = tk.NewSchema
	NewStore                     = tk.NewStore
	NewTable                     = tk.NewTable
	NewTemporalTable             = tk.NewTemporalTable
	NewTimeRange                 = tk.NewTimeRange
	NewTimeRangeOf               = tk.NewTimeRangeOf
	NewTimestampRange            = tk.NewTimestampRange
	NewUnbounded                 = tk.NewUnbounded
	Overlap                      = tk.Overlap
	Overlaps                     = tk.Overlaps
	Parse                        = tk.Parse
	ParseIntRange                = tk.ParseIntRange
	ParsePeriod                  = tk.ParsePeriod
	ParseRange                   = tk.ParseRange
	ParseTimeRange               = tk.ParseTimeRange
	Point                        = tk.Point
	PointOf                      = tk.PointOf
	RangeAdjacent                = tk.RangeAdjacent
	RangeContains                = tk.RangeContains
	RangeCoveredBy               = tk.RangeCoveredBy
	RangeDiff                    = tk.RangeDiff
	RangeDifference              = tk.RangeDifference
	RangeIntersect               = tk.RangeIntersect
	RangeIntersection            = tk.RangeIntersection
	RangeOverlap                 = tk.RangeOverlap
	RangeOverlaps                = tk.RangeOverlaps
	RangeUnion                   = tk.RangeUnion
	ResetDefault                 = tk.ResetDefault
	SameKind                     = tk.SameKind
	SortRanges                   = tk.SortRanges
	SortedKeyNames               = tk.SortedKeyNames
	SplitForPortion              = tk.SplitForPortion
	Sub                          = tk.Sub
	SubtractAll                  = tk.SubtractAll
	TimePoint                    = tk.TimePoint
	TsRange                      = tk.TsRange
	Union                        = tk.Union
	UnionAll                     = tk.UnionAll
	UnionRange                   = tk.UnionRange
	UpdatePortion                = tk.UpdatePortion
	UpperOpen                    = tk.UpperOpen
	Validate                     = tk.Validate
	ValidateAll                  = tk.ValidateAll
	ValidateCoverage             = tk.ValidateCoverage
	ValidateExclusion            = tk.ValidateExclusion
	ValidateExclusionConstraints = tk.ValidateExclusionConstraints
	ValidateFK                   = tk.ValidateFK
	ValidateForeignKey           = tk.ValidateForeignKey
)

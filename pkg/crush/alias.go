// Package crush re-exports the importable CRUSH Straw2 implementation from
// the module root for callers that prefer a .../crush import path.
package crush

import (
	root "prescreen.local/ps-7261f1735c"
)

type (
	Node                 = root.Node
	Rule                 = root.Rule
	Step                 = root.Step
	Mode                 = root.Mode
	Mapper               = root.Mapper
	Result               = root.Result
	SlotStat             = root.SlotStat
	HashFunc             = root.HashFunc
	Hash64Func           = root.Hash64Func
	Hasher               = root.Hasher
	UnsupportedHashError = root.UnsupportedHashError
	InvalidPGIDError     = root.InvalidPGIDError

	Mapping       = root.Mapping
	MapResult     = root.MapResult
	MappingResult = root.MappingResult
	Placement     = root.Placement
	SlotStats     = root.SlotStats
	SlotResult    = root.SlotResult
	SlotInfo      = root.SlotInfo
	Stat          = root.Stat

	CRUSH      = root.CRUSH
	Crush      = root.Crush
	CRUSHMap   = root.CRUSHMap
	CrushMap   = root.CrushMap
	ClusterMap = root.ClusterMap
	Topology   = root.Topology
	Bucket     = root.Bucket
	OSD        = root.OSD
	Hash       = root.Hash
)

const (
	FirstN     = root.FirstN
	Indep      = root.Indep
	ModeFirstN = root.ModeFirstN
	ModeIndep  = root.ModeIndep

	OpTake       = root.OpTake
	OpChoose     = root.OpChoose
	OpChooseLeaf = root.OpChooseLeaf
	OpEmit       = root.OpEmit

	StepTake       = root.StepTake
	StepChoose     = root.StepChoose
	StepChooseLeaf = root.StepChooseLeaf
	StepEmit       = root.StepEmit

	StepTypeTake       = root.StepTypeTake
	StepTypeChoose     = root.StepTypeChoose
	StepTypeChooseLeaf = root.StepTypeChooseLeaf
	StepTypeEmit       = root.StepTypeEmit
	RetryModeFirstN    = root.RetryModeFirstN
	RetryModeIndep     = root.RetryModeIndep

	DefaultMaxAttempts = root.DefaultMaxAttempts
)

var (
	SplitMix64   = root.SplitMix64
	DefaultHash  = root.DefaultHash
	Straw2Score  = root.Straw2Score
	Straw2Select = root.Straw2Select

	NewNode        = root.NewNode
	NewOSD         = root.NewOSD
	NewRule        = root.NewRule
	TakeStep       = root.TakeStep
	ChooseStep     = root.ChooseStep
	ChooseLeafStep = root.ChooseLeafStep
	EmitStep       = root.EmitStep

	NewMapper     = root.NewMapper
	NewMapperE    = root.NewMapperE
	New           = root.New
	MustNewMapper = root.MustNewMapper

	NewCRUSH      = root.NewCRUSH
	NewCrush      = root.NewCrush
	NewCRUSHMap   = root.NewCRUSHMap
	NewCrushMap   = root.NewCrushMap
	NewClusterMap = root.NewClusterMap
	MapPG         = root.MapPG
	Map           = root.Map
	MapOSDs       = root.MapOSDs
)

// Package diagnose is the stable diagnostic-service entry point. It forwards
// the exact rational hierarchical scheduler solver in the module root.
package diagnose

import (
	"math/big"

	root "prescreen.local/ps-8f572c36d0"
)

type (
	Problem             = root.Problem
	Input               = root.Problem
	Result              = root.SolutionSet
	Weights             = root.WeightVector
	Candidate           = root.Candidate
	WeightInterval      = root.WeightInterval
	VarSpec             = root.VarSpec
	NodeSpec            = root.NodeSpec
	ArrivalSpec         = root.ArrivalSpec
	CompressedArrival   = root.CompressedArrival
	Observation         = root.Observation
	EmissionObservation = root.EmissionObservation
	Region              = root.Region
	SolutionSet         = root.SolutionSet
	WeightVector        = root.WeightVector
	ReplayResult        = root.ReplayResult
	Emission            = root.Emission
	ClockMode           = root.ClockMode
)

const (
	ClockStartTag  = root.ClockStartTag
	ClockFinishTag = root.ClockFinishTag
	MaxWeight      = root.MaxSupportedWeight
)

func Solve(p Problem) (*SolutionSet, error) { return root.Solve(p) }

func Infer(p Problem) (*SolutionSet, error) { return root.Infer(p) }

func ContradictionPrefix(p Problem) (int, []Observation, *SolutionSet, error) {
	return root.ContradictionPrefix(p)
}

func Replay(p Problem, weights WeightVector) (*ReplayResult, error) {
	return root.Replay(p, weights)
}

func ReplayCount(p Problem, weights WeightVector, n int) (*ReplayResult, error) {
	return root.ReplayCount(p, weights, n)
}

func Verify(p Problem, weights WeightVector) (bool, error) {
	return root.Verify(p, weights)
}

func Kth(set *SolutionSet, k *big.Int) (WeightVector, error) { return set.Kth(k) }

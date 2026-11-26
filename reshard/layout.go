package reshard

import (
	"errors"
	"math/big"
)

// state is the compact, comparable encoding of a Layout inside the search
// graph. A mesh axis can never be both a sharding axis and a Partial axis.
type state struct {
	shard   []int
	partial []bool
}

func newState(l Layout) state {
	shard := append([]int(nil), l.Shard...)
	partial := append([]bool(nil), l.Partial...)
	return state{shard: shard, partial: partial}
}

func (s state) layout() Layout {
	return Layout{
		Shard:   append([]int(nil), s.shard...),
		Partial: append([]bool(nil), s.partial...),
	}
}

func (s state) clone() state {
	return state{
		shard:   append([]int(nil), s.shard...),
		partial: append([]bool(nil), s.partial...),
	}
}

func (s state) equal(o state) bool {
	if len(s.shard) != len(o.shard) || len(s.partial) != len(o.partial) {
		return false
	}
	for i := range s.shard {
		if s.shard[i] != o.shard[i] {
			return false
		}
	}
	for i := range s.partial {
		if s.partial[i] != o.partial[i] {
			return false
		}
	}
	return true
}

// validate checks a complete Problem for self-consistency and rejects any
// non-integral sharding (a dimension must be divisible by the mesh axis that
// shards it).
func (p Problem) validate() error {
	if len(p.Shape) < 1 || len(p.Shape) > 4 {
		return errors.New("reshard: tensor must have 1 to 4 dimensions")
	}
	if p.BytesPerElement <= 0 {
		return errors.New("reshard: BytesPerElement must be positive")
	}
	for i, n := range p.Shape {
		if n <= 0 {
			return errors.New("reshard: tensor dimension sizes must be positive")
		}
		_ = i
	}
	r := len(p.Mesh.Ranks)
	if r != 2 && r != 3 {
		return errors.New("reshard: device mesh must have 2 or 3 axes")
	}
	if len(p.Mesh.Weights) != r {
		return errors.New("reshard: one bandwidth weight per mesh axis is required")
	}
	for a := 0; a < r; a++ {
		if p.Mesh.Ranks[a] <= 0 {
			return errors.New("reshard: mesh axis sizes must be positive")
		}
		if p.Mesh.Weights[a] <= 0 {
			return errors.New("reshard: mesh bandwidth weights must be positive")
		}
	}
	if err := p.Source.validateLayout(p.Shape, p.Mesh.Ranks); err != nil {
		return err
	}
	if err := p.Target.validateLayout(p.Shape, p.Mesh.Ranks); err != nil {
		return err
	}
	return nil
}

func (l Layout) validateLayout(shape, ranks []int64) error {
	if len(l.Shard) != len(shape) {
		return errors.New("reshard: Layout.Shard must have one entry per tensor dimension")
	}
	meshAxes := len(ranks)
	if len(l.Partial) != meshAxes {
		return errors.New("reshard: Layout.Partial must have one entry per mesh axis")
	}
	used := make([]bool, meshAxes)
	for _, a := range l.Shard {
		if a < -1 || a >= meshAxes {
			return errors.New("reshard: sharding axis index out of range")
		}
		if a == -1 {
			continue
		}
		if used[a] {
			return errors.New("reshard: a mesh axis can shard at most one tensor dimension")
		}
		used[a] = true
		if l.Partial[a] {
			return errors.New("reshard: a mesh axis cannot be both sharding and Partial")
		}
	}
	for d, a := range l.Shard {
		if a >= 0 && shape[d]%ranks[a] != 0 {
			return errors.New("reshard: tensor dimension is not divisible by the sharding mesh axis")
		}
	}
	return nil
}

// perCardBytes returns the bytes held on one device in state s. Sharding
// divides a dimension by its axis size; Partial axes do not shrink the local
// contribution and replicated dimensions are stored in full.
func (solver *solver) perCardBytes(s state) *big.Int {
	bytes := new(big.Int).SetInt64(solver.problem.BytesPerElement)
	for d, n := range solver.problem.Shape {
		bytes.Mul(bytes, big.NewInt(n))
		if a := s.shard[d]; a >= 0 {
			bytes.Div(bytes, big.NewInt(solver.problem.Mesh.Ranks[a]))
		}
	}
	return bytes
}

// fullBytes is the total (unsharded) tensor size in bytes.
func fullBytes(p Problem) *big.Int {
	bytes := new(big.Int).SetInt64(p.BytesPerElement)
	for _, n := range p.Shape {
		bytes.Mul(bytes, big.NewInt(n))
	}
	return bytes
}

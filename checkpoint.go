package bnb

import (
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const checkpointVersion = 1

// checkpointFile is the serialized form of a Solver. It captures the
// entire frontier (every live node's path), the incumbent, and the node
// counter, so a restored solver continues the deterministic search with
// no duplicated and no missed nodes.
type checkpointFile struct {
	Version           int       `json:"version"`
	Problem           *Problem  `json:"problem"`
	IncumbentValue    int64     `json:"incumbentValue"`
	IncumbentSolution []int64   `json:"incumbentSolution"`
	NodesExplored     int64     `json:"nodesExplored"`
	Frontier          [][]int64 `json:"frontier"` // one path per live node
}

// SaveCheckpoint writes the complete solver state to w.
func (s *Solver) SaveCheckpoint(w io.Writer) error {
	cf := checkpointFile{
		Version:           checkpointVersion,
		Problem:           s.problem,
		IncumbentValue:    s.incumbentValue,
		IncumbentSolution: append([]int64(nil), s.incumbentSol...),
		NodesExplored:     s.nodesExplored,
		Frontier:          make([][]int64, len(s.frontier)),
	}
	for i, node := range s.frontier {
		cf.Frontier[i] = node.Path()
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(cf)
}

// LoadCheckpoint restores a solver previously saved with SaveCheckpoint.
// Node weights, values and bounds are recomputed from the stored paths, so
// a corrupted checkpoint is detected instead of silently trusted.
func LoadCheckpoint(r io.Reader) (*Solver, error) {
	var cf checkpointFile
	if err := json.NewDecoder(r).Decode(&cf); err != nil {
		return nil, fmt.Errorf("bnb: decoding checkpoint: %v", err)
	}
	if cf.Version != checkpointVersion {
		return nil, fmt.Errorf("bnb: unsupported checkpoint version %d", cf.Version)
	}
	if cf.Problem == nil {
		return nil, errors.New("bnb: checkpoint has no problem")
	}
	if len(cf.IncumbentSolution) != cf.Problem.NumItems() {
		return nil, fmt.Errorf("bnb: checkpoint incumbent has length %d, want %d",
			len(cf.IncumbentSolution), cf.Problem.NumItems())
	}
	s, err := NewSolver(cf.Problem)
	if err != nil {
		return nil, err
	}
	s.incumbentValue = cf.IncumbentValue
	s.incumbentSol = append([]int64(nil), cf.IncumbentSolution...)
	s.nodesExplored = cf.NodesExplored
	s.frontier = s.frontier[:0]
	for _, path := range cf.Frontier {
		node, err := s.nodeFromPath(path)
		if err != nil {
			return nil, err
		}
		heap.Push(&s.frontier, node)
	}
	return s, nil
}

// SaveCheckpointFile writes the solver state to a new file at path.
func (s *Solver) SaveCheckpointFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.SaveCheckpoint(f)
}

// LoadCheckpointFile restores a solver from a checkpoint file.
func LoadCheckpointFile(path string) (*Solver, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadCheckpoint(f)
}

// nodeFromPath rebuilds a frontier node from its path, validating that the
// path is structurally legal for the problem.
func (s *Solver) nodeFromPath(path []int64) (*Node, error) {
	p := s.problem
	if len(path) >= p.NumItems() && p.NumItems() > 0 {
		return nil, fmt.Errorf("bnb: checkpoint node path depth %d out of range", len(path))
	}
	node := &Node{path: append([]int64(nil), path...)}
	for k, x := range path {
		if x < 0 || x > p.Bounds[k] {
			return nil, fmt.Errorf("bnb: checkpoint node value %d out of bounds at depth %d", x, k)
		}
		node.weight += x * p.Weights[k]
		node.value += x * p.Values[k]
	}
	if node.weight > p.Capacity {
		return nil, errors.New("bnb: checkpoint node exceeds capacity")
	}
	node.bound = s.computeBound(node)
	return node, nil
}

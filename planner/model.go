// Package planner implements a recovery planner that schedules compressed
// block downloads, temporary decompression and atomic publishes of blocks
// arranged in a dependency DAG, minimizing peak disk usage.
package planner

import "fmt"

// ReleasePoint describes when the disk space held by a stage is freed.
type ReleasePoint string

const (
	// ReleaseOnDecompress frees the allocation when the owning block's
	// decompression finishes (default for the download stage).
	ReleaseOnDecompress ReleasePoint = "on_decompress"
	// ReleaseOnPublish frees the allocation right after the owning block's
	// atomic publish (default for the temp stage).
	ReleaseOnPublish ReleasePoint = "on_publish"
	// ReleaseAfterDependents frees the allocation once every block that
	// depends on the owning block has been published.
	ReleaseAfterDependents ReleasePoint = "after_dependents"
	// ReleaseEndOfPlan keeps the allocation until the whole plan finishes
	// (default for the final stage).
	ReleaseEndOfPlan ReleasePoint = "end_of_plan"
)

// Chunk is a named piece of disk allocation. Chunks with the same name are
// the same physical data: while live they are accounted for exactly once,
// no matter how many stages or blocks reference them.
type Chunk struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Stage is one disk allocation phase of a block. Either Size (a private,
// anonymous allocation) or Chunks (an explicit, possibly shared
// decomposition) may be set, not both.
type Stage struct {
	Size    int64        `json:"size,omitempty"`
	Chunks  []Chunk      `json:"chunks,omitempty"`
	Release ReleasePoint `json:"release,omitempty"`
}

// Block is a unit of recovery work: download the compressed block,
// decompress into a temporary area, then atomically publish the final
// content. The three stages of a block run back to back.
type Block struct {
	ID       string   `json:"id"`
	Deps     []string `json:"deps,omitempty"`
	Download Stage    `json:"download"`
	Temp     Stage    `json:"temp"`
	Final    Stage    `json:"final"`
}

// Limits bounds the search effort. Zero values mean "no limit".
type Limits struct {
	MaxExpanded int64 `json:"max_expanded,omitempty"`
	TimeoutMs   int64 `json:"timeout_ms,omitempty"`
}

// Plan is the JSON input document.
type Plan struct {
	// Budget is the disk capacity. Zero or negative means unconstrained.
	Budget int64   `json:"budget,omitempty"`
	Limits Limits  `json:"limits,omitempty"`
	Blocks []Block `json:"blocks"`
}

// Problem is the validated, normalized form of a Plan.
type Problem struct {
	Budget     int64
	blocks     []Block
	index      map[string]int
	deps       [][]int
	dependents [][]int
}

// Blocks returns the normalized blocks in input order.
func (p *Problem) Blocks() []Block { return p.blocks }

// NumBlocks returns the number of blocks.
func (p *Problem) NumBlocks() int { return len(p.blocks) }

// IndexOf maps a block ID to its internal index.
func (p *Problem) IndexOf(id string) (int, bool) {
	i, ok := p.index[id]
	return i, ok
}

// IDs converts an internal order to block IDs.
func (p *Problem) IDs(order []int) []string {
	ids := make([]string, len(order))
	for i, b := range order {
		ids[i] = p.blocks[b].ID
	}
	return ids
}

// BuildProblem validates a Plan and normalizes it into a Problem.
func BuildProblem(plan Plan) (*Problem, error) {
	p := &Problem{
		Budget:     plan.Budget,
		index:      make(map[string]int, len(plan.Blocks)),
		deps:       make([][]int, len(plan.Blocks)),
		dependents: make([][]int, len(plan.Blocks)),
	}
	chunkSize := map[string]int64{}
	for i, blk := range plan.Blocks {
		if blk.ID == "" {
			return nil, fmt.Errorf("block %d: empty id", i)
		}
		if _, dup := p.index[blk.ID]; dup {
			return nil, fmt.Errorf("duplicate block id %q", blk.ID)
		}
		p.index[blk.ID] = i
		stages := []struct {
			label string
			stage *Stage
			def   ReleasePoint
		}{
			{"download", &blk.Download, ReleaseOnDecompress},
			{"temp", &blk.Temp, ReleaseOnPublish},
			{"final", &blk.Final, ReleaseEndOfPlan},
		}
		for _, s := range stages {
			if err := normalizeStage(blk.ID, s.label, s.stage, s.def, chunkSize); err != nil {
				return nil, err
			}
		}
		p.blocks = append(p.blocks, blk)
	}
	for i, blk := range p.blocks {
		seen := map[string]bool{}
		for _, dep := range blk.Deps {
			if dep == blk.ID {
				return nil, fmt.Errorf("block %q depends on itself", blk.ID)
			}
			j, ok := p.index[dep]
			if !ok {
				return nil, fmt.Errorf("block %q: unknown dependency %q", blk.ID, dep)
			}
			if seen[dep] {
				continue
			}
			seen[dep] = true
			p.deps[i] = append(p.deps[i], j)
			p.dependents[j] = append(p.dependents[j], i)
		}
	}
	if err := p.checkAcyclic(); err != nil {
		return nil, err
	}
	return p, nil
}

func normalizeStage(blockID, label string, s *Stage, defRelease ReleasePoint, chunkSize map[string]int64) error {
	if s.Release == "" {
		s.Release = defRelease
	}
	switch s.Release {
	case ReleaseOnDecompress, ReleaseOnPublish, ReleaseAfterDependents, ReleaseEndOfPlan:
	default:
		return fmt.Errorf("block %q stage %s: invalid release point %q", blockID, label, s.Release)
	}
	if s.Size < 0 {
		return fmt.Errorf("block %q stage %s: negative size", blockID, label)
	}
	if s.Size > 0 && len(s.Chunks) > 0 {
		return fmt.Errorf("block %q stage %s: specify either size or chunks, not both", blockID, label)
	}
	if len(s.Chunks) == 0 {
		if s.Size > 0 {
			s.Chunks = []Chunk{{Name: label + ":" + blockID, Size: s.Size}}
		}
		return nil
	}
	names := map[string]bool{}
	for _, c := range s.Chunks {
		if c.Name == "" {
			return fmt.Errorf("block %q stage %s: chunk with empty name", blockID, label)
		}
		if c.Size <= 0 {
			return fmt.Errorf("block %q stage %s: chunk %q has non-positive size", blockID, label, c.Name)
		}
		if names[c.Name] {
			return fmt.Errorf("block %q stage %s: duplicate chunk %q", blockID, label, c.Name)
		}
		names[c.Name] = true
		if prev, ok := chunkSize[c.Name]; ok && prev != c.Size {
			return fmt.Errorf("chunk %q declared with conflicting sizes %d and %d", c.Name, prev, c.Size)
		}
		chunkSize[c.Name] = c.Size
	}
	return nil
}

func (p *Problem) checkAcyclic() error {
	indeg := make([]int, len(p.blocks))
	for i := range p.blocks {
		indeg[i] = len(p.deps[i])
	}
	queue := make([]int, 0, len(p.blocks))
	for i, d := range indeg {
		if d == 0 {
			queue = append(queue, i)
		}
	}
	done := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		done++
		for _, v := range p.dependents[u] {
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	if done != len(p.blocks) {
		return fmt.Errorf("dependency graph contains a cycle")
	}
	return nil
}

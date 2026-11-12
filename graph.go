package netsec

// StepGraph declares the steps a completed authorization must cover and the
// ordering between them. A step may depend on several predecessors, which
// models parallel steps that must all precede a later join step.
type StepGraph struct {
	// Steps is the complete set of required step identifiers.
	Steps []string
	// Edges maps a step to the steps that must be witnessed before it.
	Edges map[string][]string
}

// Validate checks that the graph references known steps, has no duplicates
// and contains no cycle, i.e. it is a well-formed DAG.
func (g StepGraph) Validate() error {
	if len(g.Steps) == 0 {
		return ErrEmptyGraph
	}
	indegree := make(map[string]int, len(g.Steps))
	known := make(map[string]struct{}, len(g.Steps))
	for _, step := range g.Steps {
		if step == "" {
			return ErrEmptyStepID
		}
		if _, dup := known[step]; dup {
			return duplicateStepError(step)
		}
		known[step] = struct{}{}
		indegree[step] = 0
	}
	for step, prereqs := range g.Edges {
		if _, ok := known[step]; !ok {
			return unknownEdgeStepError(step)
		}
		for _, prereq := range prereqs {
			if _, ok := known[prereq]; !ok {
				return unknownPrereqError(step, prereq)
			}
			if prereq == step {
				return selfLoopError(step)
			}
			indegree[step]++
		}
	}

	// Kahn's algorithm: a DAG drains to zero remaining nodes.
	queue := make([]string, 0, len(g.Steps))
	for step, deg := range indegree {
		if deg == 0 {
			queue = append(queue, step)
		}
	}
	remaining := len(g.Steps)
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		remaining--
		for step, prereqs := range g.Edges {
			for _, prereq := range prereqs {
				if prereq != current {
					continue
				}
				indegree[step]--
				if indegree[step] == 0 {
					queue = append(queue, step)
				}
			}
		}
	}
	if remaining != 0 {
		return ErrGraphCycle
	}
	return nil
}

// HasStep reports whether step belongs to the graph.
func (g StepGraph) HasStep(step string) bool {
	for _, candidate := range g.Steps {
		if candidate == step {
			return true
		}
	}
	return false
}

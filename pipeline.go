package vecpool

// Pipeline is a chain of vectorized operators executed over pooled chunks.
// The pipeline itself allocates nothing after construction.
type Pipeline struct {
	ops  []Operator
	pool *Pool
}

// NewPipeline builds a pipeline over the given pool. The pool must hold at
// least 2 chunks so that an operator can own an input and an output slot.
func NewPipeline(pool *Pool, ops ...Operator) *Pipeline {
	if pool.Cap() < 2 {
		panic("vecpool: pipeline needs a pool with at least 2 chunks")
	}
	return &Pipeline{ops: ops, pool: pool}
}

// Execute runs the input chunk through every operator in order and returns
// the final output chunk. Ownership of in is transferred to the pipeline
// (it is returned to the pool by the first operator); the caller owns the
// returned chunk and must return it to the pool when done.
func (p *Pipeline) Execute(in *Chunk) *Chunk {
	cur := in
	for _, op := range p.ops {
		cur = op.Process(cur, p.pool)
	}
	return cur
}

// Pool returns the pool backing the pipeline.
func (p *Pipeline) Pool() *Pool { return p.pool }

// Len returns the number of operators in the pipeline.
func (p *Pipeline) Len() int { return len(p.ops) }

package sim

// Boundary names the edge that was touched first.
type Boundary int

const (
	BoundaryNone Boundary = iota
	BoundaryUpper
	BoundaryLower
)

func (b Boundary) String() string {
	switch b {
	case BoundaryUpper:
		return "upper"
	case BoundaryLower:
		return "lower"
	}
	return "none"
}

// Conclusion is the frozen outcome of a sequential experiment.
type Conclusion struct {
	Stopped  bool
	Boundary Boundary
	Sum      int
	Steps    int    // observations consumed when the stop fired
	Evidence ObsKey // observation that first touched a boundary
}

func (c Conclusion) Equal(o Conclusion) bool {
	return c.Stopped == o.Stopped &&
		c.Boundary == o.Boundary &&
		c.Sum == o.Sum &&
		c.Steps == o.Steps &&
		c.Evidence == o.Evidence
}

// Engine accumulates integer observations against pre-given bounds and
// stops at the first touch of either edge. Once stopped the conclusion is
// frozen: later observations change nothing.
type Engine struct {
	Lower int
	Upper int

	sum   int
	steps int
	concl Conclusion
	seen  map[ObsKey]bool
}

func NewEngine(lower, upper int) *Engine {
	return &Engine{Lower: lower, Upper: upper, seen: make(map[ObsKey]bool)}
}

// Ingest consumes one observation in (Time, Seq) order. It reports true
// only on the call that first touches a boundary. Calls after the stop and
// duplicate keys are no-ops.
func (e *Engine) Ingest(ob Observation) bool {
	if e.concl.Stopped {
		return false
	}
	if e.seen[ob.Key] {
		return false
	}
	e.seen[ob.Key] = true
	e.sum += ob.Value
	e.steps++
	if e.sum >= e.Upper {
		e.concl = Conclusion{Stopped: true, Boundary: BoundaryUpper, Sum: e.sum, Steps: e.steps, Evidence: ob.Key}
		return true
	}
	if e.sum <= e.Lower {
		e.concl = Conclusion{Stopped: true, Boundary: BoundaryLower, Sum: e.sum, Steps: e.steps, Evidence: ob.Key}
		return true
	}
	return false
}

func (e *Engine) Conclusion() Conclusion { return e.concl }
func (e *Engine) Sum() int               { return e.sum }
func (e *Engine) Steps() int             { return e.steps }

// Recompute derives the conclusion from scratch over already ordered
// observations. It is the ground truth used to audit the incremental
// engine and to re-evaluate the experiment after late revisions.
func Recompute(lower, upper int, obs []Observation) Conclusion {
	e := NewEngine(lower, upper)
	for _, ob := range obs {
		e.Ingest(ob)
	}
	return e.Conclusion()
}

package flood

// Config tunes the simulation kernel.
type Config struct {
	// FlowRate is the fraction of a cell's available water that is
	// redistributed toward lower neighbours per relaxation pass (0, 1].
	FlowRate float64
	// FlowPasses is the number of relaxation passes executed per tick.
	FlowPasses int
	// Infiltration is the uniform water depth absorbed by the soil per
	// tick. Ignored for cells covered by an InfiltrationMap.
	Infiltration float64
	// BlockDepth is the water depth at or above which a cell counts as
	// flooded: it joins connected water regions and blocks roads.
	BlockDepth float64
}

func (c Config) withDefaults() Config {
	if c.FlowRate <= 0 || c.FlowRate > 1 {
		c.FlowRate = 0.5
	}
	if c.FlowPasses <= 0 {
		c.FlowPasses = 8
	}
	if c.BlockDepth <= 0 {
		c.BlockDepth = 0.1
	}
	return c
}

// Simulator evolves water depth over a DEM through discrete ticks and keeps
// a snapshot of every tick so any historical state can be queried.
type Simulator struct {
	DEM *Grid
	// InfiltrationMap optionally overrides Config.Infiltration per cell
	// (soil permeability). May be nil.
	InfiltrationMap *Grid

	Cfg Config

	rain    []float64 // rainfall depth added per tick (temporal series)
	depth   *Grid     // current water depth
	tick    int       // number of completed ticks
	history []*Grid   // history[0] = initial state, history[t] = after tick t
}

// NewSimulator builds a simulator over dem using the given rainfall time
// series (one depth value per tick; ticks beyond the series get zero rain).
func NewSimulator(dem *Grid, rain []float64, cfg Config) *Simulator {
	s := &Simulator{
		DEM:   dem,
		Cfg:   cfg.withDefaults(),
		rain:  append([]float64(nil), rain...),
		depth: NewGrid(dem.W, dem.H),
	}
	s.history = append(s.history, s.depth.Clone())
	return s
}

// Tick returns the number of completed ticks.
func (s *Simulator) Tick() int { return s.tick }

// Depth returns a copy of the current water-depth grid.
func (s *Simulator) Depth() *Grid { return s.depth.Clone() }

// DepthAt returns a copy of the water-depth grid after tick t
// (t = 0 is the initial state before any tick).
func (s *Simulator) DepthAt(t int) *Grid {
	if t < 0 {
		t = 0
	}
	if t > s.tick {
		t = s.tick
	}
	return s.history[t].Clone()
}

// TotalWater returns the total water volume after tick t.
func (s *Simulator) TotalWater(t int) float64 { return s.DepthAt(t).Sum() }

// Step advances the simulation by one tick: rainfall input, infiltration
// loss, then slope-driven overland flow relaxation.
func (s *Simulator) Step() {
	var rain float64
	if s.tick < len(s.rain) {
		rain = s.rain[s.tick]
	}
	for i := range s.depth.Cells {
		v := s.depth.Cells[i] + rain
		inf := s.Cfg.Infiltration
		if s.InfiltrationMap != nil {
			inf = s.InfiltrationMap.Cells[i]
		}
		if v -= inf; v < 0 {
			v = 0
		}
		s.depth.Cells[i] = v
	}
	for p := 0; p < s.Cfg.FlowPasses; p++ {
		s.flowPass()
	}
	s.tick++
	s.history = append(s.history, s.depth.Clone())
}

// Run advances the simulation by n ticks.
func (s *Simulator) Run(n int) {
	for i := 0; i < n; i++ {
		s.Step()
	}
}

// 8-connected neighbourhood (Moore), clockwise from north.
var (
	nbDX = [8]int{0, 1, 1, 1, 0, -1, -1, -1}
	nbDY = [8]int{-1, -1, 0, 1, 1, 1, 0, -1}
)

// flowPass performs one conservative relaxation sweep: every cell shares a
// fraction of its water with neighbours whose water surface (DEM + depth)
// is lower, proportional to the head difference. Water is conserved.
func (s *Simulator) flowPass() {
	dem, d := s.DEM, s.depth
	delta := make([]float64, len(d.Cells))
	for y := 0; y < d.H; y++ {
		for x := 0; x < d.W; x++ {
			i := y*d.W + x
			w := d.Cells[i]
			if w <= 0 {
				continue
			}
			h0 := dem.Cells[i] + w
			var headSum float64
			var heads [8]float64
			for k := 0; k < 8; k++ {
				nx, ny := x+nbDX[k], y+nbDY[k]
				if !d.InBounds(nx, ny) {
					continue
				}
				j := ny*d.W + nx
				if diff := h0 - (dem.Cells[j] + d.Cells[j]); diff > 0 {
					heads[k] = diff
					headSum += diff
				}
			}
			if headSum == 0 {
				continue
			}
			budget := w * s.Cfg.FlowRate
			for k := 0; k < 8; k++ {
				if heads[k] == 0 {
					continue
				}
				amt := budget * heads[k] / headSum
				j := (y+nbDY[k])*d.W + (x + nbDX[k])
				delta[i] -= amt
				delta[j] += amt
			}
		}
	}
	for i := range d.Cells {
		d.Cells[i] += delta[i]
		if d.Cells[i] < 0 {
			d.Cells[i] = 0
		}
	}
}

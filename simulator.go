package floodsim

// Snapshot 为某一 tick 结束时的水深场快照。
type Snapshot struct {
	Tick  int
	Depth []float64
}

// Simulator 为时空栅格洪水蔓延仿真内核。
// 每个 tick 依次执行：降雨注入 -> 渗透消减 -> 沿坡度漫延再分配。
type Simulator struct {
	Grid *Grid
	// Rain 为降雨时态序列：Rain[t] 为第 t 个 tick 全域均匀降雨水深（米）。
	Rain []float64
	// FlowRate 为漫延系数 (0, 0.5]，控制每 tick 向低处邻居转移的比例。
	FlowRate float64
	// FloodThreshold 为判定“淹没”的最小水深（米）。
	FloodThreshold float64

	depth   []float64
	history []Snapshot
	tick    int
}

// NewSimulator 创建仿真器并记录初始（tick 0）快照。
func NewSimulator(g *Grid, rain []float64) *Simulator {
	s := &Simulator{
		Grid:           g,
		Rain:           rain,
		FlowRate:       0.5,
		FloodThreshold: 0.05,
		depth:          make([]float64, g.Width*g.Height),
	}
	s.snapshot()
	return s
}

// Tick 返回当前已推进到的 tick 数（history 长度为 Tick+1）。
func (s *Simulator) Tick() int { return s.tick }

// Step 推进一个 tick。
func (s *Simulator) Step() {
	n := len(s.depth)
	// 1. 降雨注入
	if s.tick < len(s.Rain) {
		r := s.Rain[s.tick]
		for i := 0; i < n; i++ {
			s.depth[i] += r
		}
	}
	// 2. 渗透消减
	for i := 0; i < n; i++ {
		if s.depth[i] > 0 {
			if s.Grid.Infil[i] >= s.depth[i] {
				s.depth[i] = 0
			} else {
				s.depth[i] -= s.Grid.Infil[i]
			}
		}
	}
	// 3. 漫延：按水位（高程+水深）差向 4 邻低处转移，守恒且不产生负水深。
	next := make([]float64, n)
	copy(next, s.depth)
	// 每单元出流上限为其当前水深，按比例缩放避免过抽。
	out := make([]float64, n)
	type flux struct{ from, to int }
	var fluxes []flux
	amt := make([]float64, 0)
	for y := 0; y < s.Grid.Height; y++ {
		for x := 0; x < s.Grid.Width; x++ {
			i := y*s.Grid.Width + x
			if s.depth[i] <= 0 {
				continue
			}
			hi := s.Grid.Elev[i] + s.depth[i]
			for _, d := range [][2]int{{1, 0}, {0, 1}} {
				j := s.Grid.Index(x+d[0], y+d[1])
				if j < 0 {
					continue
				}
				hj := s.Grid.Elev[j] + s.depth[j]
				from, to, dh := i, j, hi-hj
				if dh < 0 {
					from, to, dh = j, i, -dh
				}
				if dh <= 0 {
					continue
				}
				f := s.FlowRate * dh / 2
				if f <= 0 {
					continue
				}
				fluxes = append(fluxes, flux{from, to})
				amt = append(amt, f)
				out[from] += f
			}
		}
	}
	for k, fl := range fluxes {
		f := amt[k]
		if out[fl.from] > s.depth[fl.from] && out[fl.from] > 0 {
			f *= s.depth[fl.from] / out[fl.from]
		}
		next[fl.from] -= f
		next[fl.to] += f
	}
	s.depth = next
	s.tick++
	s.snapshot()
}

// Run 连续推进 n 个 tick。
func (s *Simulator) Run(n int) {
	for i := 0; i < n; i++ {
		s.Step()
	}
}

func (s *Simulator) snapshot() {
	s.history = append(s.history, Snapshot{Tick: s.tick, Depth: append([]float64(nil), s.depth...)})
}

// SnapshotAt 返回历史时刻 t 的快照；t 越界时返回 nil。
func (s *Simulator) SnapshotAt(t int) *Snapshot {
	if t < 0 || t >= len(s.history) {
		return nil
	}
	return &s.history[t]
}

// DepthAt 返回历史时刻 t 的水深场（拷贝）。
func (s *Simulator) DepthAt(t int) []float64 {
	snap := s.SnapshotAt(t)
	if snap == nil {
		return nil
	}
	return append([]float64(nil), snap.Depth...)
}

// TotalWater 返回时刻 t 的总水量（用于守恒校验）。
func (s *Simulator) TotalWater(t int) float64 {
	snap := s.SnapshotAt(t)
	if snap == nil {
		return 0
	}
	sum := 0.0
	for _, d := range snap.Depth {
		sum += d
	}
	return sum
}

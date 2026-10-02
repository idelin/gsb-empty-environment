package spacetime

import (
	"errors"
	"fmt"
	"math"
)

// POI 是兴趣点(如商场柜台),用于把相遇位置解析为可读地点。
type POI struct {
	Name string
	X    float64
	Y    float64
}

// Encounter 是一次相遇接触记录:相交时段 + 地点 + 证书。
type Encounter struct {
	Interval Interval
	Location string
	MidX     float64
	MidY     float64
	Cert     Certificate
}

// Engine 是时空相交追溯引擎。
type Engine struct {
	index  *GridIndex
	trajs  map[string]*Trajectory
	prover *Prover
	pois   []POI
}

// NewEngine 创建引擎。sliceDur 为索引时间切片长度(秒),
// cellSize 为空间网格边长(米),key 为证明机构私钥,
// pois 为可选的地点兴趣点表。
func NewEngine(sliceDur, cellSize float64, key []byte, pois []POI) *Engine {
	return &Engine{
		index:  NewGridIndex(sliceDur, cellSize),
		trajs:  make(map[string]*Trajectory),
		prover: NewProver(key),
		pois:   pois,
	}
}

// Prover 返回引擎使用的证明机构,供外部核验证书。
func (e *Engine) Prover() *Prover { return e.prover }

// AddTrajectory 校验并登记一条轨迹,同时写入倒排索引。
func (e *Engine) AddTrajectory(tr *Trajectory) error {
	if err := tr.Validate(); err != nil {
		return err
	}
	e.trajs[tr.EntityID] = tr
	e.index.Add(tr)
	return nil
}

// ProveEncounters 追溯实体 aID 与 bID 在历史时空中距离小于
// threshold(米)的全部相遇接触记录,并为每一段相交时段签发
// 不可抵赖的地点证书,证书按时间顺序链接成链。
func (e *Engine) ProveEncounters(aID, bID string, threshold float64) ([]Encounter, error) {
	a, ok := e.trajs[aID]
	if !ok {
		return nil, fmt.Errorf("spacetime: unknown entity %q", aID)
	}
	b, ok := e.trajs[bID]
	if !ok {
		return nil, fmt.Errorf("spacetime: unknown entity %q", bID)
	}
	if threshold <= 0 {
		return nil, errors.New("spacetime: threshold must be positive")
	}

	// 1. 倒排索引裁剪候选时间窗。
	ranges := e.index.CandidateRanges(aID, bID, threshold)
	// 2. 候选窗内解析求交,得到精确相交时段。
	intervals := IntersectIntervals(a, b, threshold, ranges)
	// 3. 逐段解析地点并签发链式证书。
	encounters := make([]Encounter, 0, len(intervals))
	prevHash := GenesisHash
	for i, iv := range intervals {
		midT := (iv.Start + iv.End) / 2
		ax, ay, _ := a.PositionAt(midT)
		bx, by, _ := b.PositionAt(midT)
		midX, midY := (ax+bx)/2, (ay+by)/2
		location := e.nearestPOI(midX, midY)
		cert := e.prover.Issue(i+1, a, b, iv, threshold, location, midX, midY, prevHash)
		prevHash = cert.Hash
		encounters = append(encounters, Encounter{
			Interval: iv,
			Location: location,
			MidX:     midX,
			MidY:     midY,
			Cert:     cert,
		})
	}
	return encounters, nil
}

// nearestPOI 返回距 (x, y) 最近的兴趣点名称;无兴趣点表时
// 返回坐标描述。
func (e *Engine) nearestPOI(x, y float64) string {
	if len(e.pois) == 0 {
		return fmt.Sprintf("(%.1f, %.1f)", x, y)
	}
	best := e.pois[0]
	bestD := math.MaxFloat64
	for _, poi := range e.pois {
		d := (poi.X-x)*(poi.X-x) + (poi.Y-y)*(poi.Y-y)
		if d < bestD {
			bestD = d
			best = poi
		}
	}
	return best.Name
}

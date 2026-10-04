package hexgrid

import (
	"errors"
	"fmt"
	"sort"
)

// errVersionSkew 是内部信号：某次页加载返回的版本与钉住版本不一致。
var errVersionSkew = errors.New("hexgrid: version skew during query")

// Query 描述一次可达性查询。
type Query struct {
	Start Hex `json:"start"`
	K     int `json:"k"`
	// Target 可选；非 nil 时结果附带起点的最短步数与字典序决胜路径。
	Target *Hex `json:"target,omitempty"`
	// Version 可选；非 nil 时按该历史数据版本查询（旧版本查询）。
	Version *Version `json:"version,omitempty"`
}

// Stats 记录一次查询的访问量，用于观测与测试。
type Stats struct {
	PageLoads int `json:"pageLoads"` // 实际发生的按页加载次数
	Retries   int `json:"retries"`   // 因版本不一致导致的整轮重试次数
}

// Result 是查询结果。
type Result struct {
	Version   Version     `json:"version"`        // 实际使用的版本
	Reachable []Hex       `json:"reachable"`      // 可达单元，按坐标字典序
	Dist      map[Hex]int `json:"dist"`           // 每个可达单元的最短步数
	Steps     int         `json:"steps"`          // 到 Target 的最短步数；无 Target 或不可达为 -1
	Path      []Hex       `json:"path,omitempty"` // 到 Target 的一条字典序决胜最短路径
	Stats     Stats       `json:"stats"`
}

// PageSource 抽象分区页加载，便于测试注入缺页 / 异步旧页等故障。
type PageSource interface {
	LoadLatest(pid PartitionID) (Page, error)
	LoadAt(pid PartitionID, dataVersion uint64) (Page, error)
}

// Engine 在分区存储上执行查询。分区只是存储边界，邻接关系全局一致。
type Engine struct {
	Store      *Store
	Source     PageSource // 为 nil 时直接用 Store
	MaxRetries int        // 版本不一致时的最大整轮重试次数；<=0 视为 3
}

// NewEngine 创建查询引擎。
func NewEngine(s *Store) *Engine {
	return &Engine{Store: s, Source: s, MaxRetries: 3}
}

func (e *Engine) source() PageSource {
	if e.Source != nil {
		return e.Source
	}
	return e.Store
}

func (e *Engine) maxRetries() int {
	if e.MaxRetries <= 0 {
		return 3
	}
	return e.MaxRetries
}

// Run 执行查询。K=0 时只返回可通行起点；起点是障碍时报 ErrStartBlocked。
// 查询期间版本不一致会整轮重试，重试耗尽返回 ErrVersionConflict；
// 分区缺页直接返回 ErrPageMissing，绝不拼出不存在的地图。
func (e *Engine) Run(q Query) (Result, error) {
	if q.K < 0 {
		return Result{}, fmt.Errorf("hexgrid: negative K %d", q.K)
	}
	retries := 0
	for {
		pin, chunkSize := e.pinFor(q)
		res, err := e.runOnce(q, pin, chunkSize)
		if err == nil {
			res.Stats.Retries = retries
			return res, nil
		}
		if errors.Is(err, errVersionSkew) {
			if q.Version != nil {
				// 显式旧版本查询的快照不可变，不应发生漂移。
				return Result{}, fmt.Errorf("%w on pinned historical query", ErrVersionConflict)
			}
			retries++
			if retries > e.maxRetries() {
				return Result{}, ErrVersionConflict
			}
			continue
		}
		return Result{}, err
	}
}

func (e *Engine) pinFor(q Query) (Version, int) {
	cur, chunkSize := e.Store.Pin()
	if q.Version != nil {
		return Version{Data: q.Version.Data, Config: cur.Config}, chunkSize
	}
	return cur, chunkSize
}

// runOnce 在钉住的版本上跑一轮 BFS；任何页版本不一致都会失败整轮。
func (e *Engine) runOnce(q Query, pin Version, chunkSize int) (Result, error) {
	src := e.source()
	cache := map[PartitionID]Page{}
	loads := 0

	passable := func(h Hex) (bool, error) {
		pid := PartitionOf(h, chunkSize)
		page, ok := cache[pid]
		if !ok {
			var err error
			if q.Version != nil {
				page, err = src.LoadAt(pid, pin.Data)
			} else {
				page, err = src.LoadLatest(pid)
			}
			loads++
			if err != nil {
				return false, err // 含 ErrPageMissing：直接上抛，不假装是空地
			}
			if page.Version != pin {
				return false, fmt.Errorf("%w: pinned %+v, page %s at %+v",
					errVersionSkew, pin, pid, page.Version)
			}
			cache[pid] = page
		}
		return page.Cells[h], nil
	}

	dist, paths, err := bfs(q.Start, q.K, passable)
	if err != nil {
		return Result{}, err
	}

	reachable := make([]Hex, 0, len(dist))
	for h := range dist {
		reachable = append(reachable, h)
	}
	sort.Slice(reachable, func(i, j int) bool { return Compare(reachable[i], reachable[j]) < 0 })

	res := Result{
		Version:   pin,
		Reachable: reachable,
		Dist:      dist,
		Steps:     -1,
		Stats:     Stats{PageLoads: loads},
	}
	if q.Target != nil {
		if d, ok := dist[*q.Target]; ok {
			res.Steps = d
			res.Path = paths[*q.Target]
		}
	}
	return res, nil
}

// bfs 是不分区参考实现与分区引擎共用的核心：
// 逐层扩展，同层按“父路径字典序 + 邻居坐标字典序”放松，
// 因此每个单元记录的是最短步数下字典序最小的一条路径。
func bfs(start Hex, k int, passable func(Hex) (bool, error)) (map[Hex]int, map[Hex][]Hex, error) {
	ok, err := passable(start)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("%w: %v", ErrStartBlocked, start)
	}

	dist := map[Hex]int{start: 0}
	paths := map[Hex][]Hex{start: {start}}
	layer := []Hex{start}

	for d := 0; d < k && len(layer) > 0; d++ {
		// 同层按已有路径字典序处理，保证先发现者即字典序最小路径。
		sort.Slice(layer, func(i, j int) bool {
			return comparePaths(paths[layer[i]], paths[layer[j]]) < 0
		})
		var next []Hex
		for _, cur := range layer {
			nbrs := cur.Neighbors()
			sorted := nbrs[:]
			sort.Slice(sorted, func(i, j int) bool { return Compare(sorted[i], sorted[j]) < 0 })
			for _, nb := range sorted {
				if _, seen := dist[nb]; seen {
					continue
				}
				ok, err := passable(nb)
				if err != nil {
					return nil, nil, err
				}
				if !ok {
					continue
				}
				dist[nb] = d + 1
				p := make([]Hex, 0, d+2)
				p = append(p, paths[cur]...)
				p = append(p, nb)
				paths[nb] = p
				next = append(next, nb)
			}
		}
		layer = next
	}
	return dist, paths, nil
}

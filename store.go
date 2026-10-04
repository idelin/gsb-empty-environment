package hexgrid

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Version 标识一份一致的数据视图。
// Data 随每次批量通行性更新递增；Config 随分区配置换版递增。
// 查询期间两者都必须与钉住（pin）的版本一致，否则重试或拒绝。
type Version struct {
	Data   uint64 `json:"data"`
	Config uint64 `json:"config"`
}

// PartitionID 是存储分区的标识。分区只是存储边界，不改变邻接关系。
type PartitionID struct {
	PQ int `json:"pq"`
	PR int `json:"pr"`
}

func (p PartitionID) String() string { return fmt.Sprintf("p(%d,%d)", p.PQ, p.PR) }

// Page 是一次按页加载的结果：某分区在某版本下的全部已知单元。
// Cells 只包含地图中已知（有限）的单元，值为是否可通行；
// 未出现在任何页中的坐标视为不可通行，绝不凭空拼出地图。
type Page struct {
	ID      PartitionID  `json:"id"`
	Version Version      `json:"version"`
	Cells   map[Hex]bool `json:"cells"`
}

// 查询/存储错误。
var (
	// ErrPageMissing 表示分区返回缺页。调用方必须报错，不得当作空格继续。
	ErrPageMissing = errors.New("hexgrid: partition page missing")
	// ErrVersionConflict 表示重试次数耗尽仍无法获得一致版本。
	ErrVersionConflict = errors.New("hexgrid: version conflict after retries")
	// ErrUnknownVersion 表示请求的历史版本已不存在。
	ErrUnknownVersion = errors.New("hexgrid: unknown data version")
	// ErrStartBlocked 表示起点是障碍或不在可通行单元内。
	ErrStartBlocked = errors.New("hexgrid: start cell is not passable")
)

// Store 是分区的、支持多版本的单元存储。
// 它保留所有历史数据版本，因此旧版本查询永远可重放。
type Store struct {
	mu            sync.RWMutex
	chunkSize     int
	dataVersion   uint64
	configVersion uint64
	history       map[uint64]map[Hex]bool // dataVersion -> 全量单元快照

	// BeforeLoad 在每次 LoadLatest/LoadAt 加锁前调用，
	// 用于测试或模拟异步加载期间并发换版的竞态。生产环境留空。
	BeforeLoad func()
}

// NewStore 以给定分区边长（chunkSize，按 q/r 两个维度切块）和
// 初始可通行单元集合创建存储，初始版本为 {Data:1, Config:1}。
func NewStore(chunkSize int, passable []Hex) *Store {
	if chunkSize <= 0 {
		panic("hexgrid: chunkSize must be positive")
	}
	cells := make(map[Hex]bool, len(passable))
	for _, h := range passable {
		cells[h] = true
	}
	return &Store{
		chunkSize:     chunkSize,
		dataVersion:   1,
		configVersion: 1,
		history:       map[uint64]map[Hex]bool{1: cells},
	}
}

// Pin 返回当前版本与分区边长，供查询钉住一致视图。
func (s *Store) Pin() (Version, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Version{Data: s.dataVersion, Config: s.configVersion}, s.chunkSize
}

// PartitionOf 用查询钉住的 chunkSize 计算单元所属分区。
// 对负坐标使用向下取整除法，保证负坐标分区边界一致。
func PartitionOf(h Hex, chunkSize int) PartitionID {
	return PartitionID{PQ: floorDiv(h.Q, chunkSize), PR: floorDiv(h.R, chunkSize)}
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// BatchUpdate 原子地应用一批通行性变更并产生新数据版本。
// 旧版本快照保留，旧版本查询不受影响。
func (s *Store) BatchUpdate(updates map[Hex]bool) Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[Hex]bool, len(s.history[s.dataVersion]))
	for h, p := range s.history[s.dataVersion] {
		next[h] = p
	}
	for h, p := range updates {
		next[h] = p
	}
	s.dataVersion++
	s.history[s.dataVersion] = next
	return Version{Data: s.dataVersion, Config: s.configVersion}
}

// SetChunkSize 更换分区配置（换版）。只影响存储布局，不影响邻接与通行性。
func (s *Store) SetChunkSize(chunkSize int) Version {
	if chunkSize <= 0 {
		panic("hexgrid: chunkSize must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunkSize = chunkSize
	s.configVersion++
	return Version{Data: s.dataVersion, Config: s.configVersion}
}

// LoadLatest 加载某分区当前版本的页，页上盖的是存储当前版本戳。
// 若查询期间发生过更新，调用方会发现版本戳与钉住版本不一致。
func (s *Store) LoadLatest(pid PartitionID) (Page, error) {
	if s.BeforeLoad != nil {
		s.BeforeLoad()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pageLocked(pid, s.dataVersion), nil
}

// LoadAt 加载某分区在指定历史数据版本下的页（配置版本戳为当前值）。
func (s *Store) LoadAt(pid PartitionID, dataVersion uint64) (Page, error) {
	if s.BeforeLoad != nil {
		s.BeforeLoad()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.history[dataVersion]; !ok {
		return Page{}, fmt.Errorf("%w: data version %d", ErrUnknownVersion, dataVersion)
	}
	return s.pageLocked(pid, dataVersion), nil
}

func (s *Store) pageLocked(pid PartitionID, dataVersion uint64) Page {
	cells := s.history[dataVersion]
	page := Page{
		ID:      pid,
		Version: Version{Data: dataVersion, Config: s.configVersion},
		Cells:   map[Hex]bool{},
	}
	q0, q1 := pid.PQ*s.chunkSize, pid.PQ*s.chunkSize+s.chunkSize
	r0, r1 := pid.PR*s.chunkSize, pid.PR*s.chunkSize+s.chunkSize
	for h, passable := range cells {
		if h.Q >= q0 && h.Q < q1 && h.R >= r0 && h.R < r1 {
			page.Cells[h] = passable
		}
	}
	return page
}

// SortedCells 返回某数据版本下按字典序排序的全部已知单元（调试用）。
func (s *Store) SortedCells(dataVersion uint64) ([]Hex, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cells, ok := s.history[dataVersion]
	if !ok {
		return nil, fmt.Errorf("%w: data version %d", ErrUnknownVersion, dataVersion)
	}
	out := make([]Hex, 0, len(cells))
	for h := range cells {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return Compare(out[i], out[j]) < 0 })
	return out, nil
}

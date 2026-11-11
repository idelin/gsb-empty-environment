package cursorpage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"sync"
)

// Server 是模拟的远端列表服务：
//   - 游标分页采用 keyset 语义（按 Key 排序，游标记录位置键与签发代号），
//     只暴露每个键的当前版本（含墓碑）；
//   - 全量版本历史保留在服务端，可通过 (UpdatedAt, Key) 回溯接口扫描，
//     模拟真实服务的变更流/二级索引；
//   - 数据在翻页间隙被随机插入/更新/删除，删除以墓碑形式保留；
//   - 游标可以被显式过期，触发读取器退回回溯模式。
type Server struct {
	mu sync.Mutex

	rng *rand.Rand

	// current 是每键当前版本；游标分页基于它。
	current map[string]Record
	// history 保留全部历史版本（含被覆盖的旧版本与墓碑），回溯扫描基于它。
	history []Record

	versionSeq int64
	timeSeq    int64

	// 游标代号：每次签发 +1；minCursorGen 之后，旧代号游标全部过期。
	cursorGen     int64
	minCursorGen  int64
	insertCounter int64

	// 所有曾经存在过的版本号，供测试做观测性核对。
	versionHistory map[string][]int64
}

type cursorToken struct {
	Gen     int64  `json:"g"`
	LastKey string `json:"k"`
}

func NewServer(seed int64, initialKeys int) *Server {
	s := &Server{
		rng:            rand.New(rand.NewSource(seed)),
		current:        map[string]Record{},
		versionHistory: map[string][]int64{},
	}
	for i := 0; i < initialKeys; i++ {
		s.Insert(fmt.Sprintf("key-%05d", i))
	}
	return s
}

func (s *Server) encodeCursor(lastKey string) string {
	s.cursorGen++
	tok := cursorToken{Gen: s.cursorGen, LastKey: lastKey}
	raw, _ := json.Marshal(tok)
	return base64.StdEncoding.EncodeToString(raw)
}

func (s *Server) decodeCursor(cursor string) (cursorToken, error) {
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return cursorToken{}, fmt.Errorf("%w: bad token", ErrCursorExpired)
	}
	var tok cursorToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		return cursorToken{}, fmt.Errorf("%w: bad token", ErrCursorExpired)
	}
	return tok, nil
}

// ExpireCursors 让所有已签发但尚未消费完的游标立即过期。
func (s *Server) ExpireCursors() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.minCursorGen = s.cursorGen + 1
}

// ListByCursor 按 Key 升序做 keyset 分页，数据面是每键当前版本。
func (s *Server) ListByCursor(_ context.Context, cursor string, limit int) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lastKey := ""
	if cursor != "" {
		tok, err := s.decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		if tok.Gen < s.minCursorGen {
			return Page{}, fmt.Errorf("%w: generation %d below %d", ErrCursorExpired, tok.Gen, s.minCursorGen)
		}
		lastKey = tok.LastKey
	}

	keys := make([]string, 0, len(s.current))
	for k := range s.current {
		if k > lastKey {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	page := Page{}
	end := len(keys)
	if end > limit {
		end = limit
	}
	for _, k := range keys[:end] {
		page.Records = append(page.Records, s.current[k])
	}
	if end < len(keys) {
		page.NextCursor = s.encodeCursor(keys[end-1])
	}
	return page, nil
}

// ListByUpdatedAt 按 (UpdatedAt, Key, Version) 升序无状态扫描全量版本历史。
// 位置用全局严格递增的 Version 作为同刻 tie-breaker：
// 返回满足 (UpdatedAt > since) 或 (UpdatedAt == since 且 Version > versionAfter)
// 的版本；versionAfter 为 0 时包含 since 时刻的全部版本。
// 同一键的不同历史版本各自占一个位置，墓碑版本同样返回。
func (s *Server) ListByUpdatedAt(_ context.Context, since int64, versionAfter int64, limit int) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := make([]Record, 0, len(s.history))
	for _, rec := range s.history {
		if rec.UpdatedAt > since || (rec.UpdatedAt == since && rec.Version > versionAfter) {
			filtered = append(filtered, rec)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].UpdatedAt != filtered[j].UpdatedAt {
			return filtered[i].UpdatedAt < filtered[j].UpdatedAt
		}
		if filtered[i].Key != filtered[j].Key {
			return filtered[i].Key < filtered[j].Key
		}
		return filtered[i].Version < filtered[j].Version
	})

	page := Page{End: len(filtered) == 0}
	end := len(filtered)
	if end > limit {
		end = limit
	}
	page.Records = append(page.Records, filtered[:end]...)
	return page, nil
}

// bump 产生并登记一个新版本，调用方必须持有 mu。
func (s *Server) bump(key string, deleted bool) Record {
	s.versionSeq++
	s.timeSeq++
	rec := Record{
		Key:       key,
		Version:   s.versionSeq,
		UpdatedAt: s.timeSeq,
		Deleted:   deleted,
	}
	s.current[key] = rec
	s.history = append(s.history, rec)
	s.versionHistory[key] = append(s.versionHistory[key], s.versionSeq)
	return rec
}

// Insert 插入一个全新的键；键已存在时返回 false。
func (s *Server) Insert(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.current[key]; ok {
		return false
	}
	s.bump(key, false)
	return true
}

// Update 产生一个新版本（仅对存活记录生效）。
func (s *Server) Update(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.current[key]
	if !ok || rec.Deleted {
		return false
	}
	s.bump(key, false)
	return true
}

// Delete 写入墓碑版本。
func (s *Server) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.current[key]
	if !ok || rec.Deleted {
		return false
	}
	s.bump(key, true)
	return true
}

// RandomMutate 在翻页间隙随机做一次插入/更新/删除。
func (s *Server) RandomMutate() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var live []string
	for k, rec := range s.current {
		if !rec.Deleted {
			live = append(live, k)
		}
	}

	op := s.rng.Intn(3)
	if op == 0 || len(live) == 0 {
		for {
			s.insertCounter++
			key := fmt.Sprintf("ins-%d-%05d", s.rng.Int63n(1_000_000), s.insertCounter)
			if _, ok := s.current[key]; !ok {
				s.bump(key, false)
				return "insert:" + key
			}
		}
	}

	sort.Strings(live)
	target := live[s.rng.Intn(len(live))]
	if op == 1 {
		s.bump(target, false)
		return "update:" + target
	}
	s.bump(target, true)
	return "delete:" + target
}

// Snapshot 返回当前全部存活记录，按 Key 升序。
func (s *Server) Snapshot() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, rec := range s.current {
		if !rec.Deleted {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Tombstones 返回当前所有墓碑的最终版本，按 Key 升序。
func (s *Server) Tombstones() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, rec := range s.current {
		if rec.Deleted {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// VersionHistory 返回某个键经历过的全部版本号（拷贝）。
func (s *Server) VersionHistory(key string) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.versionHistory[key]...)
}

// HistorySince 返回 UpdatedAt >= since 的全部历史版本（含墓碑），按
// (UpdatedAt, Key, Version) 升序。这些版本理论上都落在回溯窗口内，
// 读取器应当全部交付过。
func (s *Server) HistorySince(since int64) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, rec := range s.history {
		if rec.UpdatedAt >= since {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt < out[j].UpdatedAt
		}
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// Stats 返回规模统计，便于测试断言与日志。
func (s *Server) Stats() (live, tombstones, versions int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.current {
		if rec.Deleted {
			tombstones++
		} else {
			live++
		}
	}
	for _, vs := range s.versionHistory {
		versions += len(vs)
	}
	return
}

package main

import "fmt"

// Record 是单个键的版本化条目。Deleted==true 表示墓碑（tombstone）。
// 删除不是移除条目，而是写入一条带更高版本的墓碑记录，
// 因此旧值不会因为迟到而“复活”已删除的键。
type Record struct {
	Key     string
	Value   string
	Version uint64
	Origin  string // 产生该版本的节点 ID，用于确定性裁决
	Deleted bool   // 墓碑标记
}

func (r Record) String() string {
	state := "live"
	if r.Deleted {
		state = "tombstone"
	}
	return fmt.Sprintf("%s=%q v%d@%s (%s)", r.Key, r.Value, r.Version, r.Origin, state)
}

// supersedes 实现显式冲突规则（全序、确定性、与到达顺序无关）：
//  1. Version 大者胜；
//  2. Version 相同，Origin 字典序大者胜；
//  3. 仍相同，墓碑胜活值（同版本删除优先）；
//  4. 仍相同，Value 字典序大者胜。
//
// 该规则使 Merge 成为交换、结合、幂等的半格 join，
// 这是安全性的核心：任意消息顺序、任意重复投递都收敛到同一状态。
func (r Record) supersedes(o Record) bool {
	if r.Version != o.Version {
		return r.Version > o.Version
	}
	if r.Origin != o.Origin {
		return r.Origin > o.Origin
	}
	if r.Deleted != o.Deleted {
		return r.Deleted
	}
	return r.Value > o.Value
}

// Store 是节点的本地键值存储（含墓碑）。
type Store struct {
	data map[string]Record
}

func NewStore() *Store { return &Store{data: map[string]Record{}} }

func (s *Store) Get(key string) (Record, bool) {
	r, ok := s.data[key]
	return r, ok
}

func (s *Store) Len() int { return len(s.data) }

// Merge 按显式规则合并一条记录，返回存储是否发生变化。
// 幂等：重复合并同一条记录不会产生变化。
func (s *Store) Merge(r Record) bool {
	cur, ok := s.data[r.Key]
	if !ok {
		s.data[r.Key] = r
		return true
	}
	if r.supersedes(cur) {
		s.data[r.Key] = r
		return true
	}
	return false
}

// Equal 判断两个存储是否完全一致（含墓碑）。
func (s *Store) Equal(o *Store) bool {
	if len(s.data) != len(o.data) {
		return false
	}
	for k, ra := range s.data {
		rb, ok := o.data[k]
		if !ok || ra != rb {
			return false
		}
	}
	return true
}

// Clone 返回存储的深拷贝。
func (s *Store) Clone() *Store {
	out := NewStore()
	for k, r := range s.data {
		out.data[k] = r
	}
	return out
}

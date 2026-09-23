package main

import "fmt"

// VersionedValue 是带单调版本号的值；版本越大越新。
type VersionedValue struct {
	Val string
	Ver int64
}

func (v VersionedValue) String() string { return fmt.Sprintf("(val=%s ver=%d)", v.Val, v.Ver) }

// Replica 是一个存储副本。所有写入（客户端写与读修复）都经过
// 同一条 last-write-wins 守卫：只接受严格更新的版本。
type Replica struct {
	ID    int
	store map[string]VersionedValue

	RejectedRepairs int // 因版本过旧而被拒绝的写入次数（安全性证据）
	// OnStore 在每次真正落盘前回调（测试用来校验单调性不变式）。
	OnStore func(key string, v VersionedValue)
}

func NewReplica(id int) *Replica {
	return &Replica{ID: id, store: map[string]VersionedValue{}}
}

// Read 返回键的当前值。
func (r *Replica) Read(key string) (VersionedValue, bool) {
	v, ok := r.store[key]
	return v, ok
}

// Apply 尝试安装一个版本；仅当 v.Ver 严格大于当前版本时才生效。
// 这是“旧修复不能覆盖新写”的唯一守卫点。
func (r *Replica) Apply(key string, v VersionedValue) bool {
	cur, ok := r.store[key]
	if ok && cur.Ver >= v.Ver {
		r.RejectedRepairs++
		return false
	}
	if r.OnStore != nil {
		r.OnStore(key, v)
	}
	r.store[key] = v
	return true
}

// Get 测试辅助：直接读取当前值（不存在时返回零值）。
func (r *Replica) Get(key string) VersionedValue { return r.store[key] }

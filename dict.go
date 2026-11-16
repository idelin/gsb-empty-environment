package netsec

import "sync"

// Dictionary 是本题约定的压缩字典扩展: 每个字典绑定唯一 ID,
// 且只能被所属租户(以其 SNI 标识)的连接选用。
type Dictionary struct {
	ID     uint32 // 线上以 uint24 编码, 0 保留表示"无字典"
	Tenant string // 拥有该字典的租户 SNI
	Data   []byte
}

// DictStore 保存各租户的压缩字典, 并发安全。
type DictStore struct {
	mu sync.RWMutex
	m  map[uint32]Dictionary
}

func NewDictStore() *DictStore {
	return &DictStore{m: make(map[uint32]Dictionary)}
}

// Add 注册字典; ID 为 0、超出 uint24 或重复时返回错误。
func (s *DictStore) Add(d Dictionary) error {
	if d.ID == 0 || d.ID > 0xFFFFFF {
		return alert(AlertInternalError, "dictionary ID %d out of uint24 range", d.ID)
	}
	if d.Tenant == "" {
		return alert(AlertInternalError, "dictionary %d has no owning tenant", d.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.m[d.ID]; exists {
		return alert(AlertInternalError, "dictionary ID %d already registered", d.ID)
	}
	s.m[d.ID] = d
	return nil
}

// forTenant 取出字典并校验归属; 字典不存在或属于其他租户时
// 返回 illegal_parameter, 且不回填字典内容。
func (s *DictStore) forTenant(id uint32, sni string) (Dictionary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.m[id]
	if !ok {
		return Dictionary{}, alert(AlertIllegalParameter, "unknown dictionary ID %d", id)
	}
	if d.Tenant != sni {
		return Dictionary{}, alert(AlertIllegalParameter,
			"dictionary %d belongs to tenant %q, not %q", id, d.Tenant, sni)
	}
	return d, nil
}

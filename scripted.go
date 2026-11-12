package netsec

import (
	"fmt"
	"sync"
)

type scriptKey struct {
	name  string
	qtype uint16
}

type scriptEntry struct {
	seq     int
	msg     *Message
	err     error
	queried int
}

// ScriptedResolver 是按 (名字, 类型) 编排应答的内存解析器，
// 用于在不接触真实网络的情况下驱动组合器的各类分支。
type ScriptedResolver struct {
	mu      sync.Mutex
	entries map[scriptKey]*scriptEntry
	nextSeq int
}

// NewScriptedResolver 创建空的脚本解析器。
func NewScriptedResolver() *ScriptedResolver {
	return &ScriptedResolver{entries: make(map[scriptKey]*scriptEntry)}
}

// SetReply 登记一次成功应答；消息会被克隆，避免测试与实现共享切片。
func (s *ScriptedResolver) SetReply(name string, qtype uint16, msg *Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[s.key(name, qtype)] = &scriptEntry{seq: s.nextSeq, msg: cloneMessage(msg)}
	s.nextSeq++
}

// SetError 登记一次失败应答（例如超时）。
func (s *ScriptedResolver) SetError(name string, qtype uint16, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[s.key(name, qtype)] = &scriptEntry{seq: s.nextSeq, err: err}
	s.nextSeq++
}

// SetTimeout 是 SetError 的语义化封装，模拟查询途中超时。
func (s *ScriptedResolver) SetTimeout(name string, qtype uint16) {
	s.SetError(name, qtype, fmt.Errorf("查询 %s/%d 超时", canonicalName(name), qtype))
}

// Query 实现 Resolver 接口。
func (s *ScriptedResolver) Query(name string, qtype uint16) (*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[s.key(name, qtype)]
	if !ok {
		return nil, fmt.Errorf("脚本解析器缺少 (%s, %d) 的编排应答", canonicalName(name), qtype)
	}
	entry.queried++
	if entry.err != nil {
		return nil, entry.err
	}
	return cloneMessage(entry.msg), nil
}

// QueryCount 返回某个 (名字, 类型) 实际被查询的次数，用于断言解析路径。
func (s *ScriptedResolver) QueryCount(name string, qtype uint16) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.entries[s.key(name, qtype)]; ok {
		return entry.queried
	}
	return 0
}

// QueriedKeys 按编排顺序返回实际发生过的查询键，便于核对信任路径。
func (s *ScriptedResolver) QueriedKeys() []scriptKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	type hit struct {
		key scriptKey
		seq int
	}
	var hits []hit
	for k, e := range s.entries {
		if e.queried > 0 {
			hits = append(hits, hit{k, e.seq})
		}
	}
	// seq 反映登记顺序；同一键不会重复登记，故可稳定还原查询顺序。
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j-1].seq > hits[j].seq; j-- {
			hits[j-1], hits[j] = hits[j], hits[j-1]
		}
	}
	out := make([]scriptKey, len(hits))
	for i, h := range hits {
		out[i] = h.key
	}
	return out
}

func (s *ScriptedResolver) key(name string, qtype uint16) scriptKey {
	return scriptKey{name: canonicalName(name), qtype: qtype}
}

func cloneMessage(in *Message) *Message {
	if in == nil {
		return nil
	}
	out := *in
	if in.Answer != nil {
		out.Answer = append([]RR(nil), in.Answer...)
	}
	if in.Authority != nil {
		out.Authority = append([]RR(nil), in.Authority...)
	}
	return &out
}

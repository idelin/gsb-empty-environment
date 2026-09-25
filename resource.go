package main

import "errors"

// ErrStaleToken fencing token 非法: 重复(相等)或回退(更小)均拒绝。
var ErrStaleToken = errors.New("stale fencing token")

// WriteRecord 一条写记录(被接受或被拒绝), 作为安全证据。
type WriteRecord struct {
	From  string
	Token int64
	Value string
	Time  int64
}

// Resource 被保护的资源: 只接受严格递增的 fencing token。
// 它不信任任何租约说法, 只认 token 的单调性 —— 这是真正的隔离点。
type Resource struct {
	id        string
	sim       *Sim
	net       *Network
	lastToken int64
	value     string
	hasValue  bool
	accepted  []WriteRecord
	rejected  []WriteRecord
}

func NewResource(id string, sim *Sim, net *Network) *Resource {
	r := &Resource{id: id, sim: sim, net: net}
	net.Register(id, r.handle)
	return r
}

// Write 应用一条携带 fencing token 的写。
// token <= lastToken 即非法: 相等为重复, 更小为回退, 二者都拒绝。
func (r *Resource) Write(from string, token int64, value string) error {
	rec := WriteRecord{From: from, Token: token, Value: value, Time: r.sim.Now()}
	if token <= r.lastToken {
		r.rejected = append(r.rejected, rec)
		r.sim.Logf("resource: 拒绝 %s 的写 token=%d value=%q (已见 token=%d, 重复/回退非法)",
			from, token, value, r.lastToken)
		return ErrStaleToken
	}
	r.lastToken = token
	r.value = value
	r.hasValue = true
	r.accepted = append(r.accepted, rec)
	r.sim.Logf("resource: 接受 %s 的写 token=%d value=%q", from, token, value)
	return nil
}

func (r *Resource) handle(m Message) {
	if m.Kind != MsgWrite {
		return
	}
	kind := MsgAck
	if err := r.Write(m.From, m.Token, m.Value); err != nil {
		kind = MsgReject
	}
	r.net.Send(Message{From: r.id, To: m.From, Kind: kind, Token: m.Token, Value: m.Value})
}

// Value 返回当前值。
func (r *Resource) Value() (string, bool) { return r.value, r.hasValue }

// Accepted 返回全部被接受的写(按接受顺序)。
func (r *Resource) Accepted() []WriteRecord { return r.accepted }

// Rejected 返回全部被拒绝的写。
func (r *Resource) Rejected() []WriteRecord { return r.rejected }

package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// Authorization 是 Registry 签发的可信降密授权,绑定请求与表达式节点,
// 只能使用一次。攻击者可以持有、复制、跨请求引用它,但无法让 Registry
// 接受非本注册器签发的对象。
type Authorization struct {
	token     uint64
	owner     *Registry
	RequestID string
	NodeID    string
	Allowed   Label
}

func (a *Authorization) String() string {
	return fmt.Sprintf("auth#%d(req=%s node=%s allow=%04x)", a.token, a.RequestID, a.NodeID, uint16(a.Allowed))
}

var (
	ErrAuthNil       = errors.New("ifc: nil authorization")
	ErrAuthForeign   = errors.New("ifc: authorization not issued by this registry")
	ErrAuthRequest   = errors.New("ifc: authorization bound to a different request")
	ErrAuthNode      = errors.New("ifc: authorization bound to a different node")
	ErrAuthExhausted = errors.New("ifc: authorization already used")
)

// Registry 签发并核销授权。所有状态变更都在同一把互斥锁内完成,
// Consume 是原子的"检查并取走",并发请求中同一授权最多被一个请求消费。
type Registry struct {
	mu   sync.Mutex
	next uint64
	live map[uint64]*Authorization
}

func NewRegistry() *Registry {
	return &Registry{live: make(map[uint64]*Authorization)}
}

// Issue 为指定请求与节点签发一次性授权。
func (r *Registry) Issue(requestID, nodeID string, allowed Label) *Authorization {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	a := &Authorization{token: r.next, owner: r, RequestID: requestID, NodeID: nodeID, Allowed: allowed}
	r.live[a.token] = a
	return a
}

// Consume 原子地校验并核销授权:必须是本注册器签发、仍存活、
// 且请求与节点完全匹配;任何一步失败都不改变注册器状态。
func (r *Registry) Consume(a *Authorization, requestID, nodeID string) error {
	if a == nil {
		return ErrAuthNil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	live, ok := r.live[a.token]
	if !ok || live != a || a.owner != r {
		// 区分"伪造/外来"与"已用完":从未签发或指针不一致视为外来。
		if a.owner != r {
			return ErrAuthForeign
		}
		return ErrAuthExhausted
	}
	if a.RequestID != requestID {
		return ErrAuthRequest
	}
	if a.NodeID != nodeID {
		return ErrAuthNode
	}
	delete(r.live, a.token)
	return nil
}

// Restore 把求值失败时已消费的授权放回注册器,实现失败不消耗授权。
// 只归还确实由本注册器签发且当前不在存活集合中的授权。
func (r *Registry) Restore(auths []*Authorization) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range auths {
		if a == nil || a.owner != r {
			continue
		}
		if _, ok := r.live[a.token]; !ok {
			r.live[a.token] = a
		}
	}
}

// Live 返回当前存活授权数,供测试与审计使用。
func (r *Registry) Live() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.live)
}

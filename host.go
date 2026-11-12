package netsec

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	// ErrForbidden 表示调用者未获得针对最终标签摘要的授权。
	ErrForbidden = errors.New("netsec: caller not authorized for final tag digest")
	// ErrRollbackFailed 表示事务失败后的回滚也失败。
	ErrRollbackFailed = errors.New("netsec: rollback failed")
)

// Authorizer 判定调用者是否被授权使用某个最终标签摘要。
// 授权绑定的是合并后的摘要，而非请求方声称的标签。
type Authorizer interface {
	Allowed(caller, digest string) bool
}

// DigestGrantSet 是基于摘要白名单的 Authorizer 实现。
type DigestGrantSet struct {
	mu     sync.RWMutex
	grants map[string]map[string]bool // caller -> digest set
}

// NewDigestGrantSet 创建空的授权集合。
func NewDigestGrantSet() *DigestGrantSet {
	return &DigestGrantSet{grants: make(map[string]map[string]bool)}
}

// Grant 授予 caller 对指定最终摘要的创建/读取权限。
func (g *DigestGrantSet) Grant(caller, digest string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.grants[caller] == nil {
		g.grants[caller] = make(map[string]bool)
	}
	g.grants[caller][digest] = true
}

// Allowed 实现 Authorizer。
func (g *DigestGrantSet) Allowed(caller, digest string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.grants[caller][digest]
}

// CreateRequest 是一次资源创建请求。
type CreateRequest struct {
	Caller string
	Tags   TagSet // 请求方希望设置的访问标签
}

// Host 是 Go 资源宿主的创建事务组件。
// 它保证：先校验并合并标签、按最终摘要完成权限检查，
// 之后才会让任何对象对外可见；任何中间步骤失败都会回滚。
type Host struct {
	store     Store
	authz     Authorizer
	inherited TagSet // 宿主级继承标签
	locked    TagSet // 不可覆盖标签
	seq       atomic.Uint64
}

// NewHost 创建宿主。inherited 与 locked 在每次创建时参与合并。
func NewHost(store Store, authz Authorizer, inherited, locked TagSet) *Host {
	return &Host{
		store:     store,
		authz:     authz,
		inherited: inherited,
		locked:    locked,
	}
}

// Create 以事务方式创建对象：
//  1. 合并并校验请求/继承/不可覆盖标签；
//  2. 按最终标签摘要做权限检查；
//  3. 分配 pending 对象（对外不可见）；
//  4. 持久化标签与摘要；
//  5. 提交，对象对外可见。
//
// 任一步骤失败都会回滚，外部读者不会观察到部分授权状态。
func (h *Host) Create(req CreateRequest) (_ string, retErr error) {
	final, err := MergeTags(req.Tags, h.inherited, h.locked)
	if err != nil {
		return "", err
	}
	digest := Digest(final)
	if !h.authz.Allowed(req.Caller, digest) {
		return "", fmt.Errorf("%w: caller=%q digest=%s", ErrForbidden, req.Caller, digest)
	}

	objID := fmt.Sprintf("obj-%d", h.seq.Add(1))
	if err := h.store.CreateObject(objID); err != nil {
		return "", fmt.Errorf("netsec: allocate object: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if derr := h.store.Delete(objID); derr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("%w: %v", ErrRollbackFailed, derr))
			}
		}
	}()

	if err := h.store.PutTags(objID, final, digest); err != nil {
		return "", fmt.Errorf("netsec: persist tags: %w", err)
	}
	if err := h.store.Commit(objID); err != nil {
		return "", fmt.Errorf("netsec: commit object: %w", err)
	}
	committed = true
	return objID, nil
}

// Read 是外部读者入口：只能看到已提交对象，
// 且读者必须被授权该对象持久化的最终标签摘要。
func (h *Host) Read(caller, id string) (Object, error) {
	obj, ok := h.store.Get(id)
	if !ok {
		return Object{}, ErrObjectNotFound
	}
	if !h.authz.Allowed(caller, obj.Digest) {
		return Object{}, fmt.Errorf("%w: caller=%q digest=%s", ErrForbidden, caller, obj.Digest)
	}
	return obj, nil
}

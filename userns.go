package netsec

import "sync/atomic"

// UserNamespace 建模内核 struct user_namespace：
// 除初始命名空间外，每个用户命名空间都有父命名空间与创建者 euid，
// 共同构成一棵以初始命名空间为根的层级树。
type UserNamespace struct {
	id       uint64
	name     string
	parent   *UserNamespace // 初始命名空间为 nil
	ownerUID uint32         // 创建者的 euid（内核 ns->owner）
}

var nsID atomic.Uint64

// NewRootUserNamespace 构造初始（init）用户命名空间，owner 为 root。
func NewRootUserNamespace() *UserNamespace {
	return &UserNamespace{id: nsID.Add(1), name: "init_user_ns", ownerUID: 0}
}

// NewChild 在 u 下创建子用户命名空间，ownerUID 为创建者 euid。
func (u *UserNamespace) NewChild(ownerUID uint32) *UserNamespace {
	return &UserNamespace{id: nsID.Add(1), parent: u, ownerUID: ownerUID}
}

// Parent 返回父用户命名空间；初始命名空间返回 nil。
func (u *UserNamespace) Parent() *UserNamespace { return u.parent }

// OwnerUID 返回创建该命名空间的 euid。
func (u *UserNamespace) OwnerUID() uint32 { return u.ownerUID }

// IsAncestorOrSelf 报告 u 是否为 other 自身或其祖先。
func (u *UserNamespace) IsAncestorOrSelf(other *UserNamespace) bool {
	for ns := other; ns != nil; ns = ns.parent {
		if ns == u {
			return true
		}
	}
	return false
}

// String 返回便于日志与测试阅读的名称。
func (u *UserNamespace) String() string {
	if u.name != "" {
		return u.name
	}
	return "user_ns"
}

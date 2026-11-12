package netsec

import "errors"

// ErrNotPermitted 对应内核返回的 -EPERM。
var ErrNotPermitted = errors.New("operation not permitted (EPERM)")

// Credentials 建模内核 struct cred 中与权限判定相关的字段。
type Credentials struct {
	UserNS     *UserNamespace // 任务当前所属用户命名空间
	Effective  CapSet         // cap_effective
	Permitted  CapSet         // cap_permitted
	EUID       uint32         // euid
	NoNewPrivs bool           // task->no_new_privs
}

// Capable 复刻内核 cap_capable() 的判定：从目标（资源所有者）命名空间
// 沿父链向上遍历，命中以下任一条件即放行：
//
//  1. 到达任务所属命名空间，且 cap 在 cap_effective 中置位；
//  2. 当前命名空间的父命名空间是任务所属命名空间，且其 owner 等于任务
//     euid（用户命名空间的创建者对其拥有全部能力）。
//
// 因此任务在子命名空间持有的 CAP_SYS_ADMIN 对父命名空间拥有的资源无效，
// 而在祖先命名空间持有的能力可以覆盖后代命名空间拥有的资源。
func (c *Credentials) Capable(target *UserNamespace, cap Capability) bool {
	for ns := target; ; ns = ns.Parent() {
		if ns == c.UserNS {
			return c.Effective.Has(cap)
		}
		if ns.Parent() == c.UserNS && ns.OwnerUID() == c.EUID {
			return true
		}
		if ns.Parent() == nil {
			return false
		}
	}
}

// UnshareUserNS 建模 unshare(CLONE_NEWUSER)：创建以任务 euid 为 owner 的
// 子用户命名空间，任务在其中获得全部 capability。这些能力仅对该子命名空间
// 及其后代拥有的资源有效。
func (c *Credentials) UnshareUserNS() *Credentials {
	child := c.UserNS.NewChild(c.EUID)
	return &Credentials{
		UserNS:     child,
		Effective:  FullCapSet,
		Permitted:  FullCapSet,
		EUID:       c.EUID,
		NoNewPrivs: c.NoNewPrivs,
	}
}

// Setns 建模 setns() 加入目标用户命名空间：要求任务对目标命名空间持有
// CAP_SYS_ADMIN；成功后任务迁入目标命名空间并获得其中的全部 capability。
// 注意能力被重新限定在目标命名空间内：迁入子命名空间后，任务对原（祖先）
// 命名空间拥有的资源不再具备权限。
func (c *Credentials) Setns(target *UserNamespace) (*Credentials, error) {
	if !c.Capable(target, CapSysAdmin) {
		return nil, ErrNotPermitted
	}
	return &Credentials{
		UserNS:     target,
		Effective:  FullCapSet,
		Permitted:  FullCapSet,
		EUID:       c.EUID,
		NoNewPrivs: c.NoNewPrivs,
	}, nil
}

// FileCaps 建模 execve 目标文件上的提权属性（setuid 位与文件 capability）。
type FileCaps struct {
	SetUID    bool   // 置 setuid 位
	UID       uint32 // setuid 目标 uid
	Permitted CapSet // 文件 permitted capability
	Effective bool   // 文件 effective 位
}

// Exec 建模 execve 后的凭据迁移。no_new_privs 置位时，setuid 位与文件
// capability 被忽略，任务无法跨 execve 获得任何新特权。
func (c *Credentials) Exec(fc *FileCaps) *Credentials {
	next := *c
	if c.NoNewPrivs || fc == nil {
		return &next
	}
	if fc.SetUID {
		next.EUID = fc.UID
	}
	next.Permitted = next.Permitted.Union(fc.Permitted)
	if fc.Effective {
		next.Effective = next.Permitted
	}
	return &next
}

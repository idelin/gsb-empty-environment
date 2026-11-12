package netsec

// 本文件定义特权操作网关：每类资源都记录其所属（拥有者）用户命名空间，
// 网关在执行前调用 Credentials.Capable 针对该命名空间做判定，
// 而不是只看任务当前的能力位。

// MountNamespace 建模挂载命名空间，owner 为创建它的用户命名空间。
type MountNamespace struct {
	Owner *UserNamespace
}

// UTSNamespace 建模 UTS 命名空间（主机名/域名）。
type UTSNamespace struct {
	Owner *UserNamespace
}

// NetNamespace 建模网络命名空间（含其 sysctl 表）。
type NetNamespace struct {
	Owner *UserNamespace
}

// Superblock 建模文件系统超级块；在其上创建设备节点需要对其所属
// 用户命名空间持有 CAP_MKNOD。
type Superblock struct {
	Owner *UserNamespace
}

// Gateway 是特权操作网关，所有方法均为纯判定，不持有状态。
type Gateway struct{}

func require(c *Credentials, owner *UserNamespace, cap Capability) error {
	if c == nil || owner == nil || !c.Capable(owner, cap) {
		return ErrNotPermitted
	}
	return nil
}

// Mount 在 target 挂载命名空间内执行挂载，需要 CAP_SYS_ADMIN。
func (Gateway) Mount(c *Credentials, target *MountNamespace, source, mountPoint, fsType string) error {
	return require(c, target.Owner, CapSysAdmin)
}

// SetHostname 修改 uts 中的主机名，需要 CAP_SYS_ADMIN。
func (Gateway) SetHostname(c *Credentials, uts *UTSNamespace, hostname string) error {
	return require(c, uts.Owner, CapSysAdmin)
}

// Mknod 在 sb 所属文件系统上创建设备节点，需要 CAP_MKNOD。
func (Gateway) Mknod(c *Credentials, sb *Superblock, path string, dev uint64) error {
	return require(c, sb.Owner, CapMknod)
}

// WriteNetSysctl 写 net 网络命名空间中的 sysctl（如 net.ipv4.ip_forward），
// 需要 CAP_NET_ADMIN。
func (Gateway) WriteNetSysctl(c *Credentials, net *NetNamespace, key, value string) error {
	return require(c, net.Owner, CapNetAdmin)
}

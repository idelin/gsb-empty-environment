package taskpkg

// 权限位：能力句柄只承载最小必要权限，且只能收窄不能放大。
const (
	PermRead  = 1 << iota // 允许在同一能力代次内读取明文
	PermWrite             // 允许在持有排他锁时替换对象内容
)

// Capability 是宿主可信打开操作签发的对象能力句柄。
//
// 能力把对象 ID、版本、租户、能力代次和读写权限绑定为一个不可分割的整体。
// 字段保持非导出：宿主进程之外的代码无法手工构造一个能力，只能通过
// Host.Open（可信打开）或 Capability.Duplicate（收窄复制）获得。
//
// 数据库文件锁不参与授权：锁仅控制读写冲突，哪怕调用方持有排他锁，
// 只要能力不匹配，宿主的读路径依然拒绝并返回 ErrAccessDenied /
// ErrWrongTenant / ErrStaleCapability。
type Capability struct {
	objectID   string
	version    int64
	tenantID   string
	generation int64
	perms      uint8
}

// Narrow 描述复制句柄时对权限的收窄动作。
//
// 每个字段为 true 表示撤销对应权限；false 表示保留。
// 结构中没有“授予”字段，因此复制结果的权限永远是父能力权限的子集。
type Narrow struct {
	DropRead  bool
	DropWrite bool
}

// ObjectID 返回能力绑定的对象 ID。
func (c Capability) ObjectID() string { return c.objectID }

// Version 返回能力签发时对象的版本号。
func (c Capability) Version() int64 { return c.version }

// TenantID 返回能力绑定的租户 ID。
func (c Capability) TenantID() string { return c.tenantID }

// Generation 返回能力绑定的能力代次。
func (c Capability) Generation() int64 { return c.generation }

// CanRead 报告能力当前是否仍带读权限。
func (c Capability) CanRead() bool { return c.perms&PermRead != 0 }

// CanWrite 报告能力当前是否仍带写权限。
func (c Capability) CanWrite() bool { return c.perms&PermWrite != 0 }

// Permissions 返回权限位掩码（PermRead / PermWrite 的组合）。
func (c Capability) Permissions() uint8 { return c.perms }

// Duplicate 复制句柄并按 narrow 收窄权限。
//
// 对象 ID、版本、租户和能力代次全部原样继承，保证副本仍由同一代次约束；
// 权限只能被撤销，任何收窄都不可能产生父能力不具备的权限。
func (c Capability) Duplicate(narrow Narrow) Capability {
	clone := c
	if narrow.DropRead {
		clone.perms &^= PermRead
	}
	if narrow.DropWrite {
		clone.perms &^= PermWrite
	}
	return clone
}

package netsec

// open_tree(2) / move_mount(2) 标志位。数值与 Linux 内核头文件保持一致，
// 使用独立的命名类型，避免两类标志在调用方混用。
const (
	OpenTreeClone     OpenTreeFlag = 0x00000001 // OPEN_TREE_CLONE
	OpenTreeRecursive OpenTreeFlag = 0x00008000 // AT_RECURSIVE
)

const (
	MoveMountFEmpty MoveMountFlag = 0x00000001 // MOVE_MOUNT_F_EMPTY_PATH
	MoveMountTEmpty MoveMountFlag = 0x00000004 // MOVE_MOUNT_T_EMPTY_PATH
)

// OpenTreeFlag 是 open_tree 调用的标志位集合。
type OpenTreeFlag uint32

// Has 报告掩码中是否包含给定标志。
func (f OpenTreeFlag) Has(flag OpenTreeFlag) bool { return f&flag != 0 }

// MoveMountFlag 是 move_mount 调用的标志位集合。
type MoveMountFlag uint32

// Has 报告掩码中是否包含给定标志。
func (f MoveMountFlag) Has(flag MoveMountFlag) bool { return f&flag != 0 }

// PropagationType 描述 mount_setattr 设置的挂载传播类型。
type PropagationType int

const (
	// PropagationUnspecified 表示事件未携带传播类型信息。
	PropagationUnspecified PropagationType = iota
	// PropagationShared 对应 MS_SHARED，挂载事件会向对等体传播。
	PropagationShared
	// PropagationPrivate 对应 MS_PRIVATE，挂载事件不传播。
	PropagationPrivate
	// PropagationSlave 对应 MS_SLAVE，只接收主端传播来的事件。
	PropagationSlave
	// PropagationUnbindable 对应 MS_UNBINDABLE，禁止再次绑定挂载。
	PropagationUnbindable
)

// InodeID 用 (设备, inode) 二元组唯一标识一次路径解析落点，
// 用来比对“检查时”与“使用时”路径是否仍指向同一个对象
// （符号链接替换等 TOCTOU 攻击会让两者分叉）。
type InodeID struct {
	Device uint64
	Inode  uint64
}

// Equal 报告两个 inode 标识是否指向同一个文件对象。
func (i InodeID) Equal(other InodeID) bool {
	return i.Device == other.Device && i.Inode == other.Inode
}

// Event 表示新挂载接口调用序列中的一个被审计事件。
// 具体类型为 OpenTreeEvent、FSMountEvent、MountSetattrEvent、
// MoveMountEvent 或 CloseEvent。
type Event interface {
	isEvent()
}

// OpenTreeEvent 对应一次 open_tree 调用。
//
// 当 Flags 含 OpenTreeClone 时，该调用克隆一棵挂载树并产生一个可作为
// move_mount 来源的描述符。CarriesShared 表示克隆出的树中（在
// AT_RECURSIVE 递归克隆的情况下）带有 shared 传播的子挂载；非递归
// 克隆无法触及更深层的 shared 子树，调用方应将其置为 false。
type OpenTreeEvent struct {
	Path          string
	Flags         OpenTreeFlag
	Descriptor    int
	CarriesShared bool
}

// FSMountEvent 对应一次 fsmount 调用，其描述符同样可作为 move_mount 来源。
// 新建的文件系统挂载默认不带入宿主已有的 shared 子树。
type FSMountEvent struct {
	Descriptor int
}

// MountSetattrEvent 对应一次 mount_setattr 调用。
//
// Recursive 为 true 表示 AT_RECURSIVE，传播修改会覆盖描述符树下的所有
// 子挂载；Propagation 为 PropagationPrivate 或 PropagationSlave 时，
// 克隆树带入的 shared 子树风险被消除。
type MountSetattrEvent struct {
	Descriptor  int
	Recursive   bool
	Propagation PropagationType
}

// MoveMountEvent 对应一次 move_mount 调用。
//
// SourceDescriptor 必须来自本序列内的 open_tree 克隆或 fsmount。
// CheckInode 是执行前对目标路径做安全检查时解析到的 inode，
// UseInode 是实际挂载时解析到的 inode；二者不同说明目标在
// 检查与使用之间被替换（典型的符号链接 TOCTOU）。
type MoveMountEvent struct {
	SourceDescriptor int
	TargetPath       string
	Flags            MoveMountFlag
	CheckInode       InodeID
	UseInode         InodeID
}

// CloseEvent 表示一个描述符被 close。关闭后再使用必须拒绝。
type CloseEvent struct {
	Descriptor int
}

func (OpenTreeEvent) isEvent()     {}
func (FSMountEvent) isEvent()      {}
func (MountSetattrEvent) isEvent() {}
func (MoveMountEvent) isEvent()    {}
func (CloseEvent) isEvent()        {}

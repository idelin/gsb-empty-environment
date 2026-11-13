// Package memfdseal 在用户态模拟 Linux memfd_create(2) 文件描述符的封印
// （sealing）状态机，供 Go 沙箱宿主在把承载配置或可执行内容的 memfd 描述符
// 传递给不可信进程之前做封印推演与交付校验。
//
// 建模的系统调用语义与内核 mm/memfd.c 的 memfd_add_seals / memfd_fcntl 一致：
//
//   - 创建时未带 MFD_ALLOW_SEALING 的 memfd 不能加任何封印，F_ADD_SEALS 返回
//     EPERM；本包用 Config.AllowSealing 表达该创建标志。
//   - F_SEAL_SEAL 一旦置位，后续任何 F_ADD_SEALS 都返回 EPERM，封印集合只能
//     增长、不能撤销。
//   - F_SEAL_WRITE 隐含 F_SEAL_SHRINK | F_SEAL_GROW；设置时若仍存在可写的
//     MAP_SHARED 映射，调用失败并返回 EBUSY，且本次封印一个都不会生效。
//   - F_SEAL_FUTURE_WRITE 同样隐含 F_SEAL_SHRINK | F_SEAL_GROW，但不检查现存
//     映射：设置之后 write(2) 与新建 PROT_WRITE|MAP_SHARED 映射分别以 EPERM、
//     EACCES 失败，可设置之前已经存在的可写共享映射仍能改写文件内容。
//   - F_SEAL_SHRINK / F_SEAL_GROW 分别阻止 ftruncate 收缩、扩张以及 write 越界
//     扩张，违例返回 EPERM。
//   - MAP_PRIVATE 的可写映射是写时复制，不修改底层文件，既不会触发 EBUSY，
//     也不阻塞交付。
//
// 交付（描述符移交）条件刻意比内核更严格：F_SEAL_WRITE、F_SEAL_SHRINK、
// F_SEAL_GROW、F_SEAL_SEAL 全部置位，且不存在任何残留的可写共享映射。
// 仅靠 F_SEAL_FUTURE_WRITE 无法满足该条件。
package memfdseal

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
)

// SealSet 是 F_SEAL_* 封印位的位集合，常量取值与 Linux UAPI fcntl.h 完全一致。
type SealSet uint32

const (
	// SealSeal 对应 F_SEAL_SEAL：禁止之后再添加任何封印。
	SealSeal SealSet = 1 << 0
	// SealShrink 对应 F_SEAL_SHRINK：禁止收缩文件。
	SealShrink SealSet = 1 << 1
	// SealGrow 对应 F_SEAL_GROW：禁止扩张文件。
	SealGrow SealSet = 1 << 2
	// SealWrite 对应 F_SEAL_WRITE：禁止写文件，且要求没有可写共享映射。
	SealWrite SealSet = 1 << 3
	// SealFutureWrite 对应 F_SEAL_FUTURE_WRITE：禁止未来的写，已有映射除外。
	SealFutureWrite SealSet = 1 << 4
)

const allKnownSeals = SealSeal | SealShrink | SealGrow | SealWrite | SealFutureWrite

// Has 报告 s 是否包含 seals 中的全部封印位。
func (s SealSet) Has(seals SealSet) bool {
	return s&seals == seals
}

// Any 报告 s 是否包含 seals 中的任意一个封印位。
func (s SealSet) Any(seals SealSet) bool {
	return s&seals != 0
}

// String 按内核封印名输出当前封印集合，空集合输出 "NONE"。
func (s SealSet) String() string {
	if s == 0 {
		return "NONE"
	}
	names := make([]string, 0, 5)
	for _, bit := range []struct {
		mask SealSet
		name string
	}{
		{SealSeal, "F_SEAL_SEAL"},
		{SealShrink, "F_SEAL_SHRINK"},
		{SealGrow, "F_SEAL_GROW"},
		{SealWrite, "F_SEAL_WRITE"},
		{SealFutureWrite, "F_SEAL_FUTURE_WRITE"},
	} {
		if s&bit.mask != 0 {
			names = append(names, bit.name)
		}
	}
	return strings.Join(names, "|")
}

// 哨兵错误均包装对应的系统 errno，可用 errors.Is(err, syscall.EBUSY) 等方式判定。
var (
	// ErrNotSealable 对应 EPERM：memfd 创建时未带 MFD_ALLOW_SEALING。
	ErrNotSealable = fmt.Errorf("memfdseal: memfd is not sealable (missing MFD_ALLOW_SEALING): %w", syscall.EPERM)
	// ErrSealLocked 对应 EPERM：F_SEAL_SEAL 已置位，不能继续加封印。
	ErrSealLocked = fmt.Errorf("memfdseal: F_SEAL_SEAL already set, no further seals allowed: %w", syscall.EPERM)
	// ErrShrinkBlocked 对应 EPERM：操作被 F_SEAL_SHRINK 阻止。
	ErrShrinkBlocked = fmt.Errorf("memfdseal: operation blocked by F_SEAL_SHRINK: %w", syscall.EPERM)
	// ErrGrowBlocked 对应 EPERM：操作被 F_SEAL_GROW 阻止。
	ErrGrowBlocked = fmt.Errorf("memfdseal: operation blocked by F_SEAL_GROW: %w", syscall.EPERM)
	// ErrWriteBlocked 对应 EPERM：write(2) 被 F_SEAL_WRITE/F_SEAL_FUTURE_WRITE 阻止。
	ErrWriteBlocked = fmt.Errorf("memfdseal: write blocked by seal: %w", syscall.EPERM)
	// ErrMapWriteBlocked 对应 EACCES：封印后新建 PROT_WRITE|MAP_SHARED 映射被拒。
	ErrMapWriteBlocked = fmt.Errorf("memfdseal: writable shared mmap blocked by seal: %w", syscall.EACCES)
	// ErrBusy 对应 EBUSY：存在可写共享映射时无法施加 F_SEAL_WRITE。
	ErrBusy = fmt.Errorf("memfdseal: F_SEAL_WRITE failed, writable shared mapping exists: %w", syscall.EBUSY)
	// ErrInvalidArgument 对应 EINVAL：未知封印位、未知映射 ID 或非法参数。
	ErrInvalidArgument = fmt.Errorf("memfdseal: invalid argument: %w", syscall.EINVAL)
	// ErrReadOnlyMapping 表示试图通过只读映射执行写入。
	ErrReadOnlyMapping = errors.New("memfdseal: mapping is not writable")
	// ErrNotDeliverable 表示不满足描述符交付条件，属于本包自定义错误，无 errno 对应。
	ErrNotDeliverable = errors.New("memfdseal: memfd not safe to deliver")
)

// Config 描述模拟 memfd 创建时的属性。
type Config struct {
	// AllowSealing 对应 memfd_create(2) 的 MFD_ALLOW_SEALING 标志；为 false 时
	// 该 memfd 永远不能添加封印。
	AllowSealing bool
	// InitialSize 可选，指定初始文件大小，默认为 0（与真实 memfd 一致）。
	InitialSize int64
}

// Mapping 描述一次存活的 mmap 映射。
type Mapping struct {
	ID       int
	Writable bool // 是否带 PROT_WRITE
	Shared   bool // true 为 MAP_SHARED，false 为 MAP_PRIVATE
	Length   int
}

// Machine 是单个 memfd 的封印状态机实例，非并发安全，按操作序列单线程驱动。
type Machine struct {
	allowSealing   bool
	size           int64
	offset         int64
	seals          SealSet
	mappings       map[int]*Mapping
	nextMappingID  int
	contentVersion uint64
	dupCount       int
}

// New 创建一个状态机，等价于一次 memfd_create(2)。
func New(cfg Config) *Machine {
	size := cfg.InitialSize
	if size < 0 {
		size = 0
	}
	return &Machine{
		allowSealing: cfg.AllowSealing,
		size:         size,
		mappings:     make(map[int]*Mapping),
	}
}

// Size 返回当前模拟文件大小。
func (m *Machine) Size() int64 { return m.size }

// Offset 返回当前文件偏移（write 成功后会前移）。
func (m *Machine) Offset() int64 { return m.offset }

// Seals 返回当前已生效封印集合的快照。
func (m *Machine) Seals() SealSet { return m.seals }

// ContentVersion 在文件内容每次被成功改写时递增，用于证明封印后内容是否仍可变化。
func (m *Machine) ContentVersion() uint64 { return m.contentVersion }

// DupCount 返回已经 dup/移交出的描述符份数。
func (m *Machine) DupCount() int { return m.dupCount }

// Mappings 返回当前存活映射的快照。
func (m *Machine) Mappings() []Mapping {
	out := make([]Mapping, 0, len(m.mappings))
	for _, mp := range m.mappings {
		out = append(out, *mp)
	}
	return out
}

// HasWritableSharedMapping 报告是否存在带 PROT_WRITE 的 MAP_SHARED 映射。
func (m *Machine) HasWritableSharedMapping() bool {
	return m.writableSharedMappingCount() > 0
}

func (m *Machine) writableSharedMappingCount() int {
	count := 0
	for _, mp := range m.mappings {
		if mp.Writable && mp.Shared {
			count++
		}
	}
	return count
}

// Truncate 模拟 ftruncate(2)：小于当前大小算收缩、大于算扩张，分别受
// F_SEAL_SHRINK / F_SEAL_GROW 约束，违例返回 ErrShrinkBlocked/ErrGrowBlocked。
func (m *Machine) Truncate(size int64) error {
	if size < 0 {
		return ErrInvalidArgument
	}
	switch {
	case size < m.size && m.seals.Has(SealShrink):
		return ErrShrinkBlocked
	case size > m.size && m.seals.Has(SealGrow):
		return ErrGrowBlocked
	}
	m.size = size
	if m.offset > size {
		m.offset = size
	}
	return nil
}

// Write 模拟从当前偏移执行 write(2)。F_SEAL_WRITE/F_SEAL_FUTURE_WRITE 置位后
// 返回 ErrWriteBlocked(EPERM)；越界扩张受 F_SEAL_GROW 约束。
func (m *Machine) Write(data []byte) error {
	if m.seals.Any(SealWrite | SealFutureWrite) {
		return ErrWriteBlocked
	}
	n := int64(len(data))
	newEnd := m.offset + n
	if newEnd > m.size {
		if m.seals.Has(SealGrow) {
			return ErrGrowBlocked
		}
		m.size = newEnd
	}
	m.offset += n
	m.contentVersion++
	return nil
}

// Mmap 模拟 mmap(2) 并返回映射 ID，供 Munmap/MapWrite 引用。封印存在时新建
// PROT_WRITE|MAP_SHARED 映射返回 ErrMapWriteBlocked(EACCES)；MAP_PRIVATE 的
// 写时复制映射始终允许。
func (m *Machine) Mmap(writable, shared bool, length int) (int, error) {
	if length <= 0 {
		return 0, ErrInvalidArgument
	}
	if writable && shared && m.seals.Any(SealWrite|SealFutureWrite) {
		return 0, ErrMapWriteBlocked
	}
	id := m.nextMappingID
	m.nextMappingID++
	m.mappings[id] = &Mapping{ID: id, Writable: writable, Shared: shared, Length: length}
	return id, nil
}

// Munmap 模拟 munmap(2)，未知映射 ID 返回 ErrInvalidArgument(EINVAL)。
func (m *Machine) Munmap(id int) error {
	if _, ok := m.mappings[id]; !ok {
		return ErrInvalidArgument
	}
	delete(m.mappings, id)
	return nil
}

// MapWrite 模拟通过既有映射写入内存。可写 MAP_SHARED 映射在 F_SEAL_FUTURE_WRITE
// 之后依旧成功并改写文件内容（ContentVersion 递增）；可写 MAP_PRIVATE 映射触发
// 写时复制，不影响底层文件；只读映射返回 ErrReadOnlyMapping。
func (m *Machine) MapWrite(id int) error {
	mp, ok := m.mappings[id]
	if !ok {
		return ErrInvalidArgument
	}
	if !mp.Writable {
		return ErrReadOnlyMapping
	}
	if mp.Shared {
		m.contentVersion++
	}
	return nil
}

// AddSeals 模拟 fcntl(fd, F_ADD_SEALS, seals)，完整复现内核的失败顺序：
// 未知封印位 -> EINVAL；不可封印 memfd -> EPERM；F_SEAL_SEAL 已置位 -> EPERM；
// 带 F_SEAL_WRITE 且存在可写共享映射 -> EBUSY（本次调用整体不生效）。
// F_SEAL_WRITE 与 F_SEAL_FUTURE_WRITE 都会隐式带上 F_SEAL_SHRINK|F_SEAL_GROW。
func (m *Machine) AddSeals(add SealSet) error {
	if add&^allKnownSeals != 0 {
		return ErrInvalidArgument
	}
	if !m.allowSealing {
		return ErrNotSealable
	}
	if m.seals.Has(SealSeal) {
		return ErrSealLocked
	}
	effective := add
	if add.Has(SealWrite) {
		if m.HasWritableSharedMapping() {
			return ErrBusy
		}
		effective |= SealShrink | SealGrow
	}
	if add.Has(SealFutureWrite) {
		effective |= SealShrink | SealGrow
	}
	m.seals |= effective
	return nil
}

// Dup 模拟不受封印门禁保护的 dup(2)，仅记录移交份数并返回最新计数。
// 正式移交描述符应使用带交付校验的 Deliver。
func (m *Machine) Dup() int {
	m.dupCount++
	return m.dupCount
}

// BlockedReasons 返回当前不能安全交付描述符的全部原因；返回空切片即满足交付条件。
func (m *Machine) BlockedReasons() []string {
	var reasons []string
	for _, missing := range []struct {
		bit  SealSet
		name string
	}{
		{SealWrite, "F_SEAL_WRITE"},
		{SealShrink, "F_SEAL_SHRINK"},
		{SealGrow, "F_SEAL_GROW"},
		{SealSeal, "F_SEAL_SEAL"},
	} {
		if !m.seals.Has(missing.bit) {
			reasons = append(reasons, "missing seal "+missing.name)
		}
	}
	if count := m.writableSharedMappingCount(); count > 0 {
		reasons = append(reasons, fmt.Sprintf("%d writable shared mapping(s) still alive", count))
	}
	return reasons
}

// Deliverable 报告交付条件是否满足：WRITE、SHRINK、GROW、SEAL 全封且无残留
// 可写共享映射。F_SEAL_FUTURE_WRITE 不能替代 F_SEAL_WRITE。
func (m *Machine) Deliverable() bool {
	return len(m.BlockedReasons()) == 0
}

// Deliver 在交付门禁通过后才模拟 dup 移交；否则返回包装了具体原因的
// ErrNotDeliverable，且不会增加移交计数。
func (m *Machine) Deliver() (int, error) {
	if reasons := m.BlockedReasons(); len(reasons) > 0 {
		return 0, fmt.Errorf("%w: %s", ErrNotDeliverable, strings.Join(reasons, "; "))
	}
	return m.Dup(), nil
}

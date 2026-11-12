package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// fsObject 是模型文件系统中的一个对象（对应真实宿主的 inode）。
type fsObject struct {
	id         ObjectID
	generation Generation
	nlink      int
}

// fsEntry 是目录中的一个名字，指向某个对象代次。
type fsEntry struct {
	parent ObjectID
	name   string
	ref    ObjectRef
}

// ModelFS 是用于测试隔离事务的内存模型文件系统。
//
// 它显式建模“对象（硬链接实体）— 目录项（名字）— 隔离映射”三层结构：
// 普通目录树中的多个名字可指向同一对象代次；对象号在对象销毁后按最小
// 可用号确定性复用，并通过代次加以区分。所有方法在互斥锁下线性化，可
// 直接用于并发测试。
type ModelFS struct {
	mu sync.Mutex

	// rootID 是根目录的对象号，根目录不会被删除或复用。
	rootID ObjectID
	// objects 按对象号保存存活对象。
	objects map[ObjectID]*fsObject
	// entries 以“父目录对象号 + 名字”为键保存目录项。
	entries map[fsEntry]*fsEntry

	// gates 保存已安装拒绝执行门禁的对象代次。
	gates map[ObjectRef]struct{}
	// quarantine 以隔离键保存被移出普通目录树的条目，保留对象引用。
	quarantine map[string]*fsEntry
	// qSeq 是隔离键的单调序号。
	qSeq int

	// freeFileIDs 是已销毁文件对象归还的可复用对象号。
	freeFileIDs []ObjectID
	// nextID 是没有可复用号时分配的新对象号，目录与文件共享同一编号空间。
	nextID ObjectID
	// maxGen 持久记录每个对象号曾分配到的最大代次，对象销毁后仍保留，
	// 保证对象号复用时新对象获得新代次，不会与旧门禁混淆。
	maxGen map[ObjectID]Generation

	// 故障注入：下一次 InstallGate 返回的错误，消费一次后清空，nil 表示不注入。
	FailInstallGate error
	// 故障注入：接下来 N 次 MoveToQuarantine 返回的错误，0 表示不注入。
	FailNextMoves int
	MoveErr       error
}

// NewModelFS 创建仅含根目录的模型文件系统。
func NewModelFS() *ModelFS {
	root := &fsObject{id: 1, generation: 1, nlink: 1}
	return &ModelFS{
		rootID:     1,
		objects:    map[ObjectID]*fsObject{1: root},
		entries:    map[fsEntry]*fsEntry{},
		gates:      map[ObjectRef]struct{}{},
		quarantine: map[string]*fsEntry{},
		nextID:     2,
		maxGen:     map[ObjectID]Generation{},
	}
}

// Root 返回根目录对象号，供构造路径时使用。
func (m *ModelFS) Root() ObjectID { return m.rootID }

// allocFileID 优先复用最小的已释放对象号，模拟真实宿主的对象号复用。
func (m *ModelFS) allocFileID() ObjectID {
	if n := len(m.freeFileIDs); n > 0 {
		id := m.freeFileIDs[0]
		m.freeFileIDs = m.freeFileIDs[1:]
		return id
	}
	id := m.nextID
	m.nextID++
	return id
}

// Create 在 parent 下创建名为 name 的新可执行对象，返回其对象代次。
func (m *ModelFS) Create(parent ObjectID, name string) ObjectRef {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLocked(parent, name)
}

func (m *ModelFS) createLocked(parent ObjectID, name string) ObjectRef {
	if _, ok := m.objects[parent]; !ok {
		panic(fmt.Sprintf("netsec: create in missing parent object %d", parent))
	}
	key := fsEntry{parent: parent, name: name}
	if _, exists := m.entries[key]; exists {
		panic(fmt.Sprintf("netsec: entry %q already exists in object %d", name, parent))
	}
	id := m.allocFileID()
	ref := ObjectRef{ID: id, Generation: m.maxGen[id] + 1}
	m.maxGen[id] = ref.Generation
	m.objects[ref.ID] = &fsObject{id: ref.ID, generation: ref.Generation, nlink: 1}
	m.entries[key] = &fsEntry{parent: parent, name: name, ref: ref}
	return ref
}

// Mkdir 在 parent 下创建名为 name 的目录，返回目录对象号。
func (m *ModelFS) Mkdir(parent ObjectID, name string) ObjectID {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[parent]; !ok {
		panic(fmt.Sprintf("netsec: mkdir in missing parent object %d", parent))
	}
	key := fsEntry{parent: parent, name: name}
	if _, exists := m.entries[key]; exists {
		panic(fmt.Sprintf("netsec: entry %q already exists in object %d", name, parent))
	}
	id := m.nextID
	m.nextID++
	m.objects[id] = &fsObject{id: id, generation: 1, nlink: 1}
	m.maxGen[id] = 1
	m.entries[key] = &fsEntry{parent: parent, name: name, ref: ObjectRef{ID: id, Generation: 1}}
	return id
}

// Link 为现有对象代次 ref 增加一个硬链接名字。
func (m *ModelFS) Link(ref ObjectRef, parent ObjectID, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[parent]; !ok {
		panic(fmt.Sprintf("netsec: link into missing parent object %d", parent))
	}
	obj, ok := m.objects[ref.ID]
	if !ok || obj.generation != ref.Generation {
		panic(fmt.Sprintf("netsec: link to missing object %s", ref))
	}
	key := fsEntry{parent: parent, name: name}
	if _, exists := m.entries[key]; exists {
		panic(fmt.Sprintf("netsec: entry %q already exists in object %d", name, parent))
	}
	obj.nlink++
	m.entries[key] = &fsEntry{parent: parent, name: name, ref: ref}
}

// Rename 在普通目录树内改名或移动目录项。
func (m *ModelFS) Rename(oldParent ObjectID, oldName string, newParent ObjectID, newName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[newParent]; !ok {
		panic(fmt.Sprintf("netsec: rename into missing parent object %d", newParent))
	}
	oldKey := fsEntry{parent: oldParent, name: oldName}
	entry, ok := m.entries[oldKey]
	if !ok {
		panic(fmt.Sprintf("netsec: missing entry %q in object %d", oldName, oldParent))
	}
	newKey := fsEntry{parent: newParent, name: newName}
	if oldKey != newKey {
		if _, exists := m.entries[newKey]; exists {
			panic(fmt.Sprintf("netsec: target entry %q already exists in object %d", newName, newParent))
		}
		delete(m.entries, oldKey)
		entry.parent = newParent
		entry.name = newName
		m.entries[newKey] = entry
	}
}

// Unlink 删除一个目录项；当对象最后一个名字消失时销毁对象并把对象号
// 归还到复用池。
func (m *ModelFS) Unlink(parent ObjectID, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unlinkLocked(parent, name)
}

func (m *ModelFS) unlinkLocked(parent ObjectID, name string) {
	key := fsEntry{parent: parent, name: name}
	entry, ok := m.entries[key]
	if !ok {
		panic(fmt.Sprintf("netsec: unlink missing entry %q in object %d", name, parent))
	}
	delete(m.entries, key)
	obj := m.objects[entry.ref.ID]
	obj.nlink--
	if obj.nlink <= 0 && entry.ref.ID != m.rootID {
		delete(m.objects, entry.ref.ID)
		m.freeFileIDs = append(m.freeFileIDs, entry.ref.ID)
		sort.Slice(m.freeFileIDs, func(i, j int) bool { return m.freeFileIDs[i] < m.freeFileIDs[j] })
	}
}

// PurgeQuarantined 销毁隔离区中的全部对象引用，模拟取证完成后的对象销毁；
// 被释放的对象号可被后续 Create 复用。
func (m *ModelFS) PurgeQuarantined() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, entry := range m.quarantine {
		obj := m.objects[entry.ref.ID]
		obj.nlink--
		if obj.nlink <= 0 && entry.ref.ID != m.rootID {
			delete(m.objects, entry.ref.ID)
			m.freeFileIDs = append(m.freeFileIDs, entry.ref.ID)
		}
		delete(m.quarantine, key)
	}
	sort.Slice(m.freeFileIDs, func(i, j int) bool { return m.freeFileIDs[i] < m.freeFileIDs[j] })
}

// Stat 返回目录项当前指向的对象代次；条目不存在时返回 ErrNameNotFound。
func (m *ModelFS) Stat(parent ObjectID, name string) (ObjectRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[fsEntry{parent: parent, name: name}]
	if !ok {
		return ObjectRef{}, ErrNameNotFound
	}
	return entry.ref, nil
}

// Graph 实现 HostFS，返回普通目录树当前完整引用图的快照。
func (m *ModelFS) Graph() RefGraph {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make([]DirEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, DirEntry{
			Parent: entry.parent,
			Name:   entry.name,
			Object: entry.ref,
		})
	}
	return RefGraph{Entries: entries}
}

// InstallGate 实现 HostFS，按对象代次安装拒绝执行门禁。
func (m *ModelFS) InstallGate(ref ObjectRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailInstallGate != nil {
		err := m.FailInstallGate
		m.FailInstallGate = nil
		return err
	}
	m.gates[ref] = struct{}{}
	return nil
}

// GateInstalled 实现 HostFS。
func (m *ModelFS) GateInstalled(ref ObjectRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.gates[ref]
	return ok
}

// RemoveGate 实现 HostFS。
func (m *ModelFS) RemoveGate(ref ObjectRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.gates, ref)
	return nil
}

// MoveToQuarantine 实现 HostFS：在校验目录项仍指向预期对象代次后，
// 原子地把名字移入隔离映射。
func (m *ModelFS) MoveToQuarantine(ref ObjectRef, parent ObjectID, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[fsEntry{parent: parent, name: name}]
	if !ok {
		return "", ErrNameNotFound
	}
	if entry.ref != ref {
		return "", ErrNameMoved
	}
	if m.FailNextMoves > 0 {
		m.FailNextMoves--
		return "", m.MoveErr
	}
	delete(m.entries, fsEntry{parent: parent, name: name})
	m.qSeq++
	key := fmt.Sprintf("quarantine-%d", m.qSeq)
	m.quarantine[key] = entry
	return key, nil
}

// RestoreFromQuarantine 实现 HostFS：把隔离条目恢复为原目录项，
// 目标位置已被占用时拒绝覆盖。
func (m *ModelFS) RestoreFromQuarantine(quarantineKey string, parent ObjectID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.quarantine[quarantineKey]
	if !ok {
		return fmt.Errorf("%w: quarantine key %q", ErrNameNotFound, quarantineKey)
	}
	if _, ok := m.objects[parent]; !ok {
		return fmt.Errorf("%w: parent object %d", ErrNameNotFound, parent)
	}
	target := fsEntry{parent: parent, name: name}
	if _, exists := m.entries[target]; exists {
		return errors.New("netsec: restore destination name already exists")
	}
	delete(m.quarantine, quarantineKey)
	entry.parent = parent
	entry.name = name
	m.entries[target] = entry
	return nil
}

// ExecAllowed 模拟宿主执行路径上的门禁检查：只有对象代次精确命中
// 已安装门禁时拒绝执行。对象号复用后的新代次不受旧门禁影响。
func (m *ModelFS) ExecAllowed(ref ObjectRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, blocked := m.gates[ref]
	return !blocked
}

// ExecutableAt 模拟对 (parent, name) 名字发起执行：先解析当前对象代次，
// 再经过门禁检查。条目不存在时视为不可执行。
func (m *ModelFS) ExecutableAt(parent ObjectID, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[fsEntry{parent: parent, name: name}]
	if !ok {
		return false
	}
	_, blocked := m.gates[entry.ref]
	return !blocked
}

// QuarantineLen 返回隔离映射中的条目数，供测试断言。
func (m *ModelFS) QuarantineLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.quarantine)
}

// GateCount 返回当前生效的门禁数量，供测试断言。
func (m *ModelFS) GateCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.gates)
}

// EntryExists 报告普通目录树中是否存在指定目录项。
func (m *ModelFS) EntryExists(parent ObjectID, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.entries[fsEntry{parent: parent, name: name}]
	return ok
}

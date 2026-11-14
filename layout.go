package netsec

import (
	"fmt"
	"math/big"
	"sort"
)

// 基础类型标识，与 Solidity 的 elementary 类型对齐（仅建模升级核对所需类型）。
const (
	TypeAddress = "address"
	TypeBool    = "bool"
	TypeBytes20 = "bytes20"
	TypeBytes32 = "bytes32"
	TypeUint8   = "uint8"
	TypeUint64  = "uint64"
	TypeUint128 = "uint128"
	TypeUint256 = "uint256"
	TypeInt256  = "int256"
	TypeMapping = "mapping"
)

// elementarySize 返回基础类型按 EVM 存储打包占用的字节数；未知类型返回 0。
func elementarySize(kind string) uint {
	switch kind {
	case TypeAddress, TypeBytes20:
		return 20
	case TypeBool, TypeUint8:
		return 1
	case TypeUint64:
		return 8
	case TypeUint128:
		return 16
	case TypeBytes32, TypeUint256, TypeInt256:
		return 32
	case TypeMapping:
		return 32 // mapping 仅保留自身声明槽，条目数据存放在 keccak 派生槽
	default:
		return 0
	}
}

// Entry 描述一个存储变量在单个 32 字节槽内的占用情况。
//
// Slot 为槽号；Offset 为槽内低序端起的字节偏移（与 Solidity 打包偏移一致）；
// Size 为占用字节数；Contract 为声明该字段的合约名；Order 为该合约字段声明序号。
type Entry struct {
	Name     string
	Type     string
	Slot     *big.Int
	Offset   uint
	Size     uint
	Contract string
	Order    int
}

func (e Entry) position() position {
	return position{slot: e.Slot.String(), offset: e.Offset}
}

type position struct {
	slot   string
	offset uint
}

// Layout 是一份已编译的存储布局，字段顺序与合约继承（C3 线性化）后的存储顺序一致。
type Layout struct {
	Contracts []string // 线性化后的合约顺序：最基类在前，最派生合约在最后
	Entries   []Entry  // 按声明顺序排列的存储变量
}

// EntryByName 按变量名查找字段，未找到时返回 false。
func (l *Layout) EntryByName(name string) (Entry, bool) {
	for _, entry := range l.Entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return Entry{}, false
}

// maxUsedSlot 返回布局占用的最大槽号；空布局返回 -1。
func (l *Layout) maxUsedSlot() *big.Int {
	max := big.NewInt(-1)
	for _, entry := range l.Entries {
		if entry.Slot.Cmp(max) > 0 {
			max.Set(entry.Slot)
		}
	}
	return new(big.Int).Set(max)
}

// LayoutBuilder 按 Solidity 存储打包规则构造布局，并在构造期防御性地检测重叠。
//
// 每个合约通过 BeginContract 声明；继承顺序必须按 C3 线性化结果从基类到派生类依次声明，
// 基类存储先于派生存储分配，从而保证“继承顺序”可被升级前核对。
type LayoutBuilder struct {
	contracts []string
	entries   []Entry

	currentContract string
	contractOrder   int
	cursorSlot      *big.Int
	cursorOffset    uint
	seenNames       map[string]bool
}

// NewLayoutBuilder 创建构造器，存储从 0 号槽开始分配。
func NewLayoutBuilder() *LayoutBuilder {
	return &LayoutBuilder{
		cursorSlot: new(big.Int),
		seenNames:  make(map[string]bool),
	}
}

// BeginContract 开始一个合约的字段声明段。合约必须按线性化顺序（基类在前）加入。
func (b *LayoutBuilder) BeginContract(name string) *LayoutBuilder {
	if name != "" {
		b.contracts = append(b.contracts, name)
		b.currentContract = name
		b.contractOrder = 0
	}
	return b
}

// Add 按 Solidity 规则追加一个 elementary 或 mapping 字段，并返回新字段。
// 同一槽剩余空间足够且类型可打包时复用当前槽，否则前进到下一槽。
func (b *LayoutBuilder) Add(name, kind string) (Entry, error) {
	size := elementarySize(kind)
	if size == 0 {
		return Entry{}, fmt.Errorf("netsec: 不支持的存储类型 %q（字段 %s）", kind, name)
	}
	if name == "" {
		return Entry{}, fmt.Errorf("netsec: 存储字段必须命名")
	}
	if b.seenNames[name] {
		return Entry{}, fmt.Errorf("netsec: 存储字段名 %q 重复", name)
	}

	slot := new(big.Int).Set(b.cursorSlot)
	offset := b.cursorOffset
	if kind == TypeMapping || offset+size > 32 {
		slot.Add(slot, big.NewInt(1))
		offset = 0
	}

	entry := Entry{
		Name:     name,
		Type:     kind,
		Slot:     slot,
		Offset:   offset,
		Size:     size,
		Contract: b.currentContract,
		Order:    b.contractOrder,
	}
	b.contractOrder++
	b.entries = append(b.entries, entry)
	b.seenNames[name] = true

	b.cursorSlot = new(big.Int).Set(slot)
	if kind == TypeMapping {
		// mapping 不参与打包，下一字段从新槽开始。
		b.cursorSlot.Add(b.cursorSlot, big.NewInt(1))
		b.cursorOffset = 0
	} else {
		b.cursorOffset = offset + size
		if b.cursorOffset == 32 {
			b.cursorSlot.Add(b.cursorSlot, big.NewInt(1))
			b.cursorOffset = 0
		}
	}
	return entry, nil
}

// Build 输出版本化布局，并在输出前执行完整的槽位冲突与保留槽位检测。
func (b *LayoutBuilder) Build() (*Layout, []Finding) {
	layout := &Layout{
		Contracts: append([]string(nil), b.contracts...),
		Entries:   append([]Entry(nil), b.entries...),
	}
	findings := DetectSlotConflicts(layout)
	if len(findings) > 0 {
		return layout, findings
	}
	return layout, nil
}

// DetectSlotConflicts 检测两类槽位冲突：
//
//   - 同槽字节区间重叠（字段错位的静态形态，含 mapping/32 字节字段独占冲突）；
//   - 业务字段落在 EIP-1967 保留槽位（管理员、实现、beacon）。
func DetectSlotConflicts(layout *Layout) []Finding {
	var findings []Finding

	occupied := map[string][]Entry{}
	for _, entry := range layout.Entries {
		key := entry.Slot.String()
		occupied[key] = append(occupied[key], entry)
		if slot, ok := reservedSlotByName(entry.Slot); ok {
			findings = append(findings, Finding{
				State:   StateSlotConflict,
				Message: fmt.Sprintf("字段 %s 占用 EIP-1967 保留槽位 %s（%s）", entry.Name, entry.Slot.Text(16), slot),
				Entry:   entry.Name,
			})
		}
	}

	slotKeys := make([]string, 0, len(occupied))
	for key := range occupied {
		slotKeys = append(slotKeys, key)
	}
	sort.Slice(slotKeys, func(i, j int) bool {
		left, _ := new(big.Int).SetString(slotKeys[i], 10)
		right, _ := new(big.Int).SetString(slotKeys[j], 10)
		return left.Cmp(right) < 0
	})

	for _, key := range slotKeys {
		group := occupied[key]
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				if rangesOverlap(group[i], group[j]) {
					findings = append(findings, Finding{
						State: StateSlotConflict,
						Message: fmt.Sprintf(
							"槽位 %s 上字段 %s 与 %s 字节区间重叠",
							group[i].Slot.Text(16), group[i].Name, group[j].Name),
						Entry: group[i].Name,
					})
				}
			}
		}
	}

	sortFindings(findings)
	return findings
}

func rangesOverlap(a, b Entry) bool {
	if a.Slot.Cmp(b.Slot) != 0 {
		return false
	}
	return a.Offset < b.Offset+b.Size && b.Offset < a.Offset+a.Size
}

// CheckInheritance 核对新布局中共同基类的相对顺序不变。
// 派生实现合约允许替换（如 VaultV1 -> VaultV2），但基类调换会整体平移字段，直接造成错位。
func CheckInheritance(oldLayout, newLayout *Layout) []Finding {
	if len(oldLayout.Contracts) == 0 {
		return nil
	}
	index := make(map[string]int, len(newLayout.Contracts))
	for i, name := range newLayout.Contracts {
		index[name] = i
	}

	var findings []Finding
	lastSeen := -1
	for _, name := range oldLayout.Contracts {
		pos, ok := index[name]
		if !ok {
			// 派生实现合约本身可能被替换；仅当多个旧合约同时缺失才说明线性化被破坏。
			continue
		}
		if pos < lastSeen {
			findings = append(findings, Finding{
				State:   StateLayoutMisaligned,
				Message: fmt.Sprintf("合约 %s 在新布局中的继承顺序前移，基类存储顺序变化会平移后续槽位", name),
				Entry:   name,
			})
		}
		lastSeen = pos
	}
	if len(findings) == 0 {
		shared := 0
		for _, name := range oldLayout.Contracts {
			if _, ok := index[name]; ok {
				shared++
			}
		}
		if shared == 0 {
			findings = append(findings, Finding{
				State:   StateLayoutMisaligned,
				Message: "新布局与旧布局没有任何共同基类，继承线性化顺序无法核对",
				Entry:   oldLayout.Contracts[0],
			})
		}
	}
	sortFindings(findings)
	return findings
}

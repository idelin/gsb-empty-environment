package netsec

import (
	"math/big"
	"testing"
)

func mustAddress(t *testing.T, hex string) Address {
	t.Helper()
	addr, err := AddressFromHex(hex)
	if err != nil {
		t.Fatalf("地址解析失败: %v", err)
	}
	return addr
}

func mustBuild(t *testing.T, builder *LayoutBuilder) *Layout {
	t.Helper()
	layout, findings := builder.Build()
	if len(findings) != 0 {
		t.Fatalf("布局构造出现意外冲突: %+v", findings)
	}
	return layout
}

func TestBuilderPackingAndInheritance(t *testing.T) {
	builder := NewLayoutBuilder().BeginContract("Ownable")

	admin, err := builder.Add("owner", TypeAddress) // 20 字节，归属 Ownable
	if err != nil {
		t.Fatal(err)
	}
	paused, err := builder.Add("paused", TypeBool) // 1 字节，同槽打包
	if err != nil {
		t.Fatal(err)
	}
	builder.BeginContract("Vault")
	balance, err := builder.Add("balance", TypeUint256) // 剩余 11 字节放不下，进新槽
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := builder.Add("accounts", TypeMapping) // mapping 独占新槽
	if err != nil {
		t.Fatal(err)
	}
	layout := mustBuild(t, builder)

	if admin.Slot.Cmp(big.NewInt(0)) != 0 || admin.Offset != 0 || admin.Contract != "Ownable" {
		t.Fatalf("owner 打包位置错误: %+v", admin)
	}
	if paused.Slot.Cmp(big.NewInt(0)) != 0 || paused.Offset != 20 || paused.Contract != "Ownable" {
		t.Fatalf("paused 应与 owner 同槽打包: %+v", paused)
	}
	if balance.Slot.Cmp(big.NewInt(1)) != 0 || balance.Offset != 0 || balance.Contract != "Vault" {
		t.Fatalf("balance 应进入 1 号槽: %+v", balance)
	}
	if accounts.Slot.Cmp(big.NewInt(3)) != 0 || accounts.Type != TypeMapping {
		t.Fatalf("mapping 应独占 3 号槽: %+v", accounts)
	}
	if len(layout.Contracts) != 2 || layout.Contracts[0] != "Ownable" || layout.Contracts[1] != "Vault" {
		t.Fatalf("继承线性化顺序错误: %v", layout.Contracts)
	}

	if _, err := NewLayoutBuilder().Add("bad", "uint999"); err == nil {
		t.Fatal("未知类型应报错")
	}
}

func TestDetectSlotConflictsOverlap(t *testing.T) {
	// 手工构造攻击者制造的错位布局：两个字段在同一槽字节区间重叠，
	// 管理员槽语义会被相邻的“余额”字段读错。
	layout := &Layout{
		Contracts: []string{"MaliciousImpl"},
		Entries: []Entry{
			{Name: "owner", Type: TypeAddress, Slot: big.NewInt(0), Offset: 0, Size: 20},
			{Name: "userBalance", Type: TypeUint256, Slot: big.NewInt(0), Offset: 12, Size: 32},
		},
	}
	findings := DetectSlotConflicts(layout)
	if len(findings) != 1 || findings[0].State != StateSlotConflict || !findings[0].Blocking() {
		t.Fatalf("应检测到同槽重叠冲突，实际: %+v", findings)
	}
}

func TestDetectReservedSlotCollision(t *testing.T) {
	// 实现合约把业务变量声明到 EIP-1967 实现槽，直接与代理核心槽冲突。
	layout := &Layout{
		Contracts: []string{"EvilImpl"},
		Entries: []Entry{
			{Name: "evilFlag", Type: TypeBytes32, Slot: new(big.Int).Set(ImplementationSlot), Offset: 0, Size: 32},
		},
	}
	findings := DetectSlotConflicts(layout)
	if len(findings) != 1 || findings[0].State != StateSlotConflict {
		t.Fatalf("应检测到 EIP-1967 保留槽冲突，实际: %+v", findings)
	}

	// 普通布局不会触碰任何保留槽。
	cleanBuilder := NewLayoutBuilder().BeginContract("Vault")
	if _, err := cleanBuilder.Add("owner", TypeAddress); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanBuilder.Add("balance", TypeUint256); err != nil {
		t.Fatal(err)
	}
	clean := mustBuild(t, cleanBuilder)
	if findings := DetectSlotConflicts(clean); len(findings) != 0 {
		t.Fatalf("正常布局不应有冲突: %+v", findings)
	}
}

func TestCheckInheritanceReorder(t *testing.T) {
	oldLayout := &Layout{Contracts: []string{"A", "B", "C"}}
	good := &Layout{Contracts: []string{"A", "B", "C", "D"}}
	if findings := CheckInheritance(oldLayout, good); len(findings) != 0 {
		t.Fatalf("保持相对顺序的继承扩展应通过: %+v", findings)
	}

	swapped := &Layout{Contracts: []string{"B", "A", "C"}}
	findings := CheckInheritance(oldLayout, swapped)
	if len(findings) == 0 || findings[0].State != StateLayoutMisaligned {
		t.Fatalf("基类顺序调换应判布局错位，实际: %+v", findings)
	}

	// 所有共同基类都消失（实现合约整体替换为另一条继承链），无法核对线性化。
	unrelated := &Layout{Contracts: []string{"X", "Y"}}
	findings = CheckInheritance(oldLayout, unrelated)
	if len(findings) != 1 || findings[0].State != StateLayoutMisaligned {
		t.Fatalf("无共同基类应判布局错位，实际: %+v", findings)
	}
}

func TestStoragePackedFieldAccess(t *testing.T) {
	storage := NewProxyStorage()
	storageBuilder := NewLayoutBuilder().BeginContract("Vault")
	if _, err := storageBuilder.Add("owner", TypeAddress); err != nil {
		t.Fatal(err)
	}
	if _, err := storageBuilder.Add("paused", TypeBool); err != nil {
		t.Fatal(err)
	}
	layout := mustBuild(t, storageBuilder)
	owner, _ := layout.EntryByName("owner")
	paused, _ := layout.EntryByName("paused")

	admin := mustAddress(t, "0x1111111111111111111111111111111111111111")
	if err := storage.SetField(owner, new(big.Int).SetBytes(admin[:])); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetField(paused, big.NewInt(1)); err != nil {
		t.Fatal(err)
	}
	if storage.GetField(paused).Int64() != 1 {
		t.Fatal("回读 paused 错误")
	}
	if storage.GetField(owner).Cmp(new(big.Int).SetBytes(admin[:])) != 0 {
		t.Fatal("回读 owner 错误，打包写入互相污染")
	}

	if err := storage.SetField(paused, big.NewInt(256)); err == nil {
		t.Fatal("超宽写入应被拒绝")
	}
}

func TestMappingSlotDerivation(t *testing.T) {
	// Solidity: mapping(address => uint) accounts; 声明槽 3，键为某地址。
	holder := mustAddress(t, "0x2222222222222222222222222222222222222222")
	key := new(big.Int).SetBytes(holder[:])
	slot := MappingSlot(big.NewInt(3), key)
	if slot.Cmp(big.NewInt(3)) == 0 {
		t.Fatal("mapping 条目槽必须派生到声明槽之外")
	}
	if MappingSlot(big.NewInt(3), key).Cmp(slot) != 0 {
		t.Fatal("相同声明槽与键必须派生相同槽位")
	}
}

package netsec

import (
	"errors"
	"math/big"
	"testing"
)

// buildV1 构造初始实现布局：owner(20B)+paused(1B) 打包在槽 0，balance 占槽 1。
func buildV1(t *testing.T) *Layout {
	t.Helper()
	builder := NewLayoutBuilder().BeginContract("Ownable")
	for _, field := range []struct{ name, kind string }{
		{"owner", TypeAddress},
		{"paused", TypeBool},
	} {
		if _, err := builder.Add(field.name, field.kind); err != nil {
			t.Fatal(err)
		}
	}
	builder.BeginContract("VaultV1")
	if _, err := builder.Add("balance", TypeUint256); err != nil {
		t.Fatal(err)
	}
	return mustBuild(t, builder)
}

func TestPlanLayoutMisalignedAndUnmigrated(t *testing.T) {
	oldLayout := buildV1(t)

	// 攻击者实现把槽 0 偏移 0 重新解释为 userBalance：管理员槽被读成用户余额。
	evilLayout := &Layout{
		Contracts: []string{"Ownable", "VaultV2"},
		Entries: []Entry{
			{Name: "userBalance", Type: TypeUint256, Slot: big.NewInt(0), Offset: 0, Size: 32},
			{Name: "balance", Type: TypeUint256, Slot: big.NewInt(1), Offset: 0, Size: 32},
		},
	}

	report, err := Plan(oldLayout, evilLayout, nil)
	if err != nil {
		t.Fatalf("Plan 返回错误而非报告: %v", err)
	}
	if !report.Blocked {
		t.Fatal("错位升级必须被阻断")
	}
	if !hasState(report, StateLayoutMisaligned) || !hasState(report, StateUnmigratedUpgrade) {
		t.Fatalf("应同时给出错位与未迁移状态，实际: %+v", report.Findings)
	}

	storage := NewProxyStorage()
	if _, err := Apply(storage, oldLayout, evilLayout, mustAddress(t, "0xabc0000000000000000000000000000000000001"), nil); !errors.Is(err, ErrUpgradeBlocked) {
		t.Fatalf("Apply 应返回 ErrUpgradeBlocked，实际: %v", err)
	}
}

func TestPlanSlotConflictBlocks(t *testing.T) {
	oldLayout := buildV1(t)
	conflictLayout := &Layout{
		Contracts: []string{"Ownable", "VaultV2"},
		Entries: []Entry{
			{Name: "owner", Type: TypeAddress, Slot: big.NewInt(0), Offset: 0, Size: 20},
			{Name: "paused", Type: TypeBool, Slot: big.NewInt(0), Offset: 20, Size: 1},
			{Name: "balance", Type: TypeUint256, Slot: new(big.Int).Set(AdminSlot), Offset: 0, Size: 32},
		},
	}
	report, err := Plan(oldLayout, conflictLayout, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != StateSlotConflict || !report.Blocked {
		t.Fatalf("保留槽冲突应给出 SLOT_CONFLICT 并阻断，实际: %+v", report)
	}
}

func TestPlanLegalAppendOnlyUpgrade(t *testing.T) {
	oldLayout := buildV1(t)
	// 合法升级：原有槽位/类型/顺序完全不动，只在尾部未使用槽位追加新字段。
	builder := NewLayoutBuilder().BeginContract("Ownable")
	for _, field := range []struct{ name, kind string }{
		{"owner", TypeAddress},
		{"paused", TypeBool},
	} {
		if _, err := builder.Add(field.name, field.kind); err != nil {
			t.Fatal(err)
		}
	}
	builder.BeginContract("VaultV2")
	for _, field := range []struct{ name, kind string }{
		{"balance", TypeUint256},
		{"treasury", TypeAddress},
	} {
		if _, err := builder.Add(field.name, field.kind); err != nil {
			t.Fatal(err)
		}
	}
	newLayout := mustBuild(t, builder)

	report, err := Plan(oldLayout, newLayout, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked || report.State != StateLegalUpgrade {
		t.Fatalf("纯追加升级应判合法，实际: %+v", report.Findings)
	}
	if len(report.Changes) != 1 || report.Changes[0].Kind != "appended" {
		t.Fatalf("应登记一条 appended 变更，实际: %+v", report.Changes)
	}
}

func TestUnmigratedInsertRejected(t *testing.T) {
	oldLayout := buildV1(t)
	// 在已使用区间内插入新字段会把后续槽位整体后移，属于必须迁移的错位变更。
	insertedLayout := &Layout{
		Contracts: []string{"Ownable", "VaultV2"},
		Entries: []Entry{
			{Name: "owner", Type: TypeAddress, Slot: big.NewInt(0), Offset: 0, Size: 20},
			{Name: "paused", Type: TypeBool, Slot: big.NewInt(0), Offset: 20, Size: 1},
			{Name: "nonce", Type: TypeUint64, Slot: big.NewInt(1), Offset: 0, Size: 8},
			{Name: "balance", Type: TypeUint256, Slot: big.NewInt(2), Offset: 0, Size: 32},
		},
	}
	report, err := Plan(oldLayout, insertedLayout, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Blocked || !hasState(report, StateUnmigratedUpgrade) {
		t.Fatalf("中间插入且无迁移必须判未迁移升级，实际: %+v", report.Findings)
	}
}

func TestApplyWithExplicitMigrationAndReadback(t *testing.T) {
	oldLayout := buildV1(t)

	// 新实现重排存储：balance 移到槽 2，槽 1 新增初始化的 nonce，paused 被删除。
	newLayout := &Layout{
		Contracts: []string{"Ownable", "VaultV2"},
		Entries: []Entry{
			{Name: "owner", Type: TypeAddress, Slot: big.NewInt(0), Offset: 0, Size: 20},
			{Name: "nonce", Type: TypeUint64, Slot: big.NewInt(1), Offset: 0, Size: 8},
			{Name: "balance", Type: TypeUint256, Slot: big.NewInt(2), Offset: 0, Size: 32},
		},
	}

	storage := NewProxyStorage()
	admin := mustAddress(t, "0x1111111111111111111111111111111111111111")
	oldImpl := mustAddress(t, "0x000000000000000000000000000000000000aaaa")
	newImpl := mustAddress(t, "0x000000000000000000000000000000000000bbbb")
	InitializeProxy(storage, admin, oldImpl)

	ownerEntry, _ := oldLayout.EntryByName("owner")
	balanceEntry, _ := oldLayout.EntryByName("balance")
	if err := storage.SetField(ownerEntry, new(big.Int).SetBytes(admin[:])); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetField(balanceEntry, big.NewInt(1_000_000)); err != nil {
		t.Fatal(err)
	}

	// 没有显式迁移时必须被阻断。
	if _, err := Apply(storage, oldLayout, newLayout, newImpl, nil); !errors.Is(err, ErrUpgradeBlocked) {
		t.Fatalf("未迁移升级应被阻断，实际: %v", err)
	}

	migrations := []Migration{
		{Op: MigrateMove, Field: "balance", FromField: "balance", Reason: "槽位后移"},
		{Op: MigrateInitialize, Field: "nonce", Value: big.NewInt(7), Reason: "新增重放保护"},
		{Op: MigrateClear, Field: "paused", Reason: "移除暂停开关"},
	}

	// 迁移引用字段错误时应被 validateMigrations 拒绝。
	badMigrations := []Migration{{Op: MigrateMove, Field: "balance", FromField: "ghost"}}
	if _, err := Plan(oldLayout, newLayout, badMigrations); err == nil {
		t.Fatal("引用不存在字段的迁移必须报错")
	}

	upgradeReport, readback, err := ApplyAndVerify(storage, oldLayout, newLayout, admin, newImpl, migrations)
	if err != nil {
		t.Fatalf("合法迁移升级失败: %v", err)
	}
	if upgradeReport.State != StateLegalUpgrade || upgradeReport.Blocked {
		t.Fatalf("升级报告应为合法升级，实际: %+v", upgradeReport)
	}
	if readback.State != StateReadbackVerified || !readback.Passed {
		t.Fatalf("回读应通过，实际: %+v", readback.Findings)
	}

	// 关键不变量：管理员槽保持不变、实现槽已切换、迁移值可在新槽回读。
	if storage.GetAddress(AdminSlot) != admin {
		t.Fatal("升级不得改写管理员槽")
	}
	if storage.GetAddress(ImplementationSlot) != newImpl {
		t.Fatal("实现槽应切换到新实现地址")
	}
	newBalance, _ := newLayout.EntryByName("balance")
	if storage.GetField(newBalance).Int64() != 1_000_000 {
		t.Fatal("balance 未按 MOVE 迁移到新槽")
	}
	nonceEntry, _ := newLayout.EntryByName("nonce")
	if storage.GetField(nonceEntry).Int64() != 7 {
		t.Fatal("nonce 未按 INITIALIZE 写入")
	}

	// 回读报告逐字段携带期望值与观测值。
	for _, field := range readback.Fields {
		if field.Name == "balance" && (field.Expected.Int64() != 1_000_000 || !field.Matched) {
			t.Fatalf("balance 回读明细错误: %+v", field)
		}
	}
}

func TestReadbackDetectsCorruption(t *testing.T) {
	layout := buildV1(t)
	storage := NewProxyStorage()
	admin := mustAddress(t, "0x1111111111111111111111111111111111111111")
	impl := mustAddress(t, "0x000000000000000000000000000000000000bbbb")
	InitializeProxy(storage, admin, impl)

	ownerEntry, _ := layout.EntryByName("owner")
	balanceEntry, _ := layout.EntryByName("balance")
	if err := storage.SetField(ownerEntry, new(big.Int).SetBytes(admin[:])); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetField(balanceEntry, big.NewInt(42)); err != nil {
		t.Fatal(err)
	}

	// 预期值与存储不一致（模拟迁移写坏或字段错位），回读必须失败并保持同一状态枚举。
	expected := map[string]*big.Int{
		"owner":   new(big.Int).SetBytes(admin[:]),
		"balance": big.NewInt(42),
		"paused":  big.NewInt(0),
	}
	readback, err := ReadbackVerify(storage, layout, admin, impl, expected)
	if err != nil {
		t.Fatal(err)
	}
	if !readback.Passed {
		t.Fatal("一致数据应回读通过")
	}

	expected["balance"] = big.NewInt(999)
	readback, err = ReadbackVerify(storage, layout, admin, impl, expected)
	if err != nil {
		t.Fatal(err)
	}
	if readback.Passed || readback.State != StateReadbackVerified {
		t.Fatal("回读失败仍须使用 READBACK_VERIFIED 状态并将 Passed 置 false")
	}
	if !hasReadbackFinding(readback, "balance") {
		t.Fatalf("应包含 balance 字段的回读失败发现: %+v", readback.Findings)
	}

	// 管理员槽被改写同样必须被回读捕获。
	readback, _ = ReadbackVerify(storage, layout, mustAddress(t, "0x0000000000000000000000000000000000000009"), impl, expected)
	if readback.Passed {
		t.Fatal("管理员槽不一致必须导致回读失败")
	}
}

func hasState(report *UpgradeReport, state State) bool {
	for _, finding := range report.Findings {
		if finding.State == state {
			return true
		}
	}
	return false
}

func hasReadbackFinding(report *ReadbackReport, entry string) bool {
	for _, finding := range report.Findings {
		if finding.State == StateReadbackVerified && finding.Entry == entry {
			return true
		}
	}
	return false
}

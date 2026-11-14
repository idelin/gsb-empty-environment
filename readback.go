package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// ReadbackField 是单个关键字段的回读比对结果。
type ReadbackField struct {
	Name     string
	Slot     *big.Int
	Offset   uint
	Size     uint
	Expected *big.Int
	Observed *big.Int
	Matched  bool
}

// ReadbackReport 是升级后回读验证报告，状态恒为 READBACK_VERIFIED，Passed 表示是否通过。
type ReadbackReport struct {
	State         State
	Passed        bool
	AdminExpected Address
	AdminObserved Address
	ImplExpected  Address
	ImplObserved  Address
	Fields        []ReadbackField
	Findings      []Finding
}

// ReadbackVerify 回读 EIP-1967 管理员/实现槽位与关键字段，比对预期值。
// expected 中列出的字段做值比对；未列出的字段仅做可解码性回读。
func ReadbackVerify(storage *ProxyStorage, layout *Layout, admin, implementation Address, expected map[string]*big.Int) (*ReadbackReport, error) {
	if storage == nil || layout == nil {
		return nil, errors.New("netsec: 存储与布局不能为 nil")
	}
	report := &ReadbackReport{
		State:         StateReadbackVerified,
		Passed:        true,
		AdminExpected: admin,
		ImplExpected:  implementation,
	}
	report.AdminObserved = storage.GetAddress(AdminSlot)
	report.ImplObserved = storage.GetAddress(ImplementationSlot)
	if report.AdminObserved != admin {
		report.Passed = false
		report.Findings = append(report.Findings, Finding{
			State:   StateReadbackVerified,
			Message: fmt.Sprintf("管理员槽回读值 %s 与预期 %s 不一致", report.AdminObserved.Hex(), admin.Hex()),
			Entry:   "admin",
		})
	}
	if report.ImplObserved != implementation {
		report.Passed = false
		report.Findings = append(report.Findings, Finding{
			State:   StateReadbackVerified,
			Message: fmt.Sprintf("实现槽回读值 %s 与预期 %s 不一致", report.ImplObserved.Hex(), implementation.Hex()),
			Entry:   "implementation",
		})
	}

	if conflictFindings := DetectSlotConflicts(layout); len(conflictFindings) > 0 {
		report.Passed = false
		report.Findings = append(report.Findings, conflictFindings...)
	}

	for _, entry := range layout.Entries {
		observed := storage.GetField(entry)
		field := ReadbackField{
			Name:     entry.Name,
			Slot:     new(big.Int).Set(entry.Slot),
			Offset:   entry.Offset,
			Size:     entry.Size,
			Observed: observed,
			Matched:  true,
		}
		if want, ok := expected[entry.Name]; ok {
			field.Expected = new(big.Int).Set(want)
			if observed.Cmp(want) != 0 {
				field.Matched = false
				report.Passed = false
				report.Findings = append(report.Findings, Finding{
					State: StateReadbackVerified,
					Message: fmt.Sprintf("关键字段 %s 回读值 %s 与迁移预期 %s 不一致",
						entry.Name, observed.Text(10), want.Text(10)),
					Entry: entry.Name,
				})
			}
		}
		report.Fields = append(report.Fields, field)
	}

	sortFindings(report.Findings)
	return report, nil
}

// ApplyAndVerify 执行升级，并依据迁移记录独立推导每个字段的迁移预期，再做全量回读验证。
// 预期值在执行前从旧存储快照推导，因此回读能发现迁移写坏、槽位错位或管理员被改写等问题。
func ApplyAndVerify(storage *ProxyStorage, oldLayout, newLayout *Layout, admin, implementation Address, migrations []Migration) (*UpgradeReport, *ReadbackReport, error) {
	expected := buildExpected(storage, oldLayout, newLayout, migrations)
	report, err := Apply(storage, oldLayout, newLayout, implementation, migrations)
	if err != nil {
		return report, nil, err
	}
	readback, err := ReadbackVerify(storage, newLayout, admin, implementation, expected)
	if err != nil {
		return report, nil, err
	}
	if !readback.Passed {
		report.State = StateReadbackVerified
	}
	return report, readback, nil
}

// InitializeProxy 在首次部署时写入管理员与实现地址，二者均落在 EIP-1967 保留槽位。
func InitializeProxy(storage *ProxyStorage, admin, implementation Address) {
	storage.SetAddress(AdminSlot, admin)
	storage.SetAddress(ImplementationSlot, implementation)
}

// buildExpected 在执行迁移前推导新布局每个字段升级后应当读到的值。
func buildExpected(storage *ProxyStorage, oldLayout, newLayout *Layout, migrations []Migration) map[string]*big.Int {
	oldByName := indexByName(oldLayout)
	expected := map[string]*big.Int{}

	// 默认：同名字段沿用旧值；新字段默认零值。
	for _, entry := range newLayout.Entries {
		if oldEntry, ok := oldByName[entry.Name]; ok {
			expected[entry.Name] = storage.GetField(oldEntry)
		} else {
			expected[entry.Name] = big.NewInt(0)
		}
	}

	for _, migration := range migrations {
		switch migration.Op {
		case MigrateMove:
			expected[migration.Field] = storage.GetField(oldByName[migration.FromField])
		case MigrateClear:
			expected[migration.Field] = big.NewInt(0)
		case MigrateInitialize:
			expected[migration.Field] = new(big.Int).Set(migration.Value)
		}
	}
	return expected
}

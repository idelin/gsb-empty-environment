package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// State 是升级核对与回读验证的统一状态枚举，所有代码路径共用同一套解释。
type State string

const (
	// StateLayoutMisaligned 表示新布局在已使用槽位上错位：字段位置/类型变化或继承顺序被打乱。
	// 它是风险事实而非必然阻断：所有错位都被显式迁移覆盖时升级仍合法。
	StateLayoutMisaligned State = "LAYOUT_MISALIGNED"
	// StateSlotConflict 表示同槽字节区间重叠，或业务字段占用 EIP-1967 保留槽位。
	StateSlotConflict State = "SLOT_CONFLICT"
	// StateUnmigratedUpgrade 表示存在需要迁移的布局变更，却没有显式迁移覆盖。
	StateUnmigratedUpgrade State = "UNMIGRATED_UPGRADE"
	// StateLegalUpgrade 表示布局兼容、无冲突，且所有变更均被显式迁移覆盖。
	StateLegalUpgrade State = "LEGAL_UPGRADE"
	// StateReadbackVerified 表示升级后关键字段回读完成；Passed 区分通过与失败。
	StateReadbackVerified State = "READBACK_VERIFIED"
)

// Blocking 报告该状态是否会阻止升级执行。
func (s State) Blocking() bool {
	switch s {
	case StateSlotConflict, StateUnmigratedUpgrade:
		return true
	default:
		return false
	}
}

// Finding 是一条核对发现，状态字段决定解释口径，Entry 为相关字段或合约名。
type Finding struct {
	State   State
	Message string
	Entry   string
}

// Blocking 报告该发现是否阻止升级。
func (f Finding) Blocking() bool { return f.State.Blocking() }

// MigrationOp 标识显式迁移的操作类型。
type MigrationOp string

const (
	// MigrateMove 把旧字段值搬到新字段（允许跨槽位、跨偏移）。
	MigrateMove MigrationOp = "MOVE"
	// MigrateClear 清空字段所在字节区间，用于删除字段或腾空被复用的槽位。
	MigrateClear MigrationOp = "CLEAR"
	// MigrateInitialize 给新插入字段写入确定的初始值。
	MigrateInitialize MigrationOp = "INITIALIZE"
)

// Migration 描述一条显式存储迁移。布局变更没有对应迁移即构成未迁移升级。
type Migration struct {
	Op        MigrationOp
	Field     string // 目标字段名（MOVE 的目的、CLEAR/INITIALIZE 的作用字段）
	FromField string // 仅 MOVE 使用：旧布局中的源字段名
	Value     *big.Int
	Reason    string
}

// Change 记录一条被核对接受的布局变更及其覆盖迁移，用于审计与回读预期推导。
type Change struct {
	Kind      string
	Field     string
	Detail    string
	Migration *Migration
}

// UpgradeReport 是升级前核对与升级执行的统一报告。
type UpgradeReport struct {
	State      State
	Blocked    bool
	Findings   []Finding
	Changes    []Change
	OldLayout  *Layout
	NewLayout  *Layout
	Migrations []Migration
}

// ErrUpgradeBlocked 在核对发现阻断性问题、升级不得执行时返回。
var ErrUpgradeBlocked = errors.New("netsec: 升级被存储布局核对阻断")

// Plan 在升级前核对两份布局：槽位、类型、继承顺序与显式迁移覆盖情况。
//
// 仅向未使用槽位尾部追加字段属于兼容升级；任何触及已使用槽位的变化都判定为布局错位，
// 必须由显式迁移覆盖，否则同时给出 LAYOUT_MISALIGNED 与 UNMIGRATED_UPGRADE 发现。
func Plan(oldLayout, newLayout *Layout, migrations []Migration) (*UpgradeReport, error) {
	if oldLayout == nil || newLayout == nil {
		return nil, errors.New("netsec: 新旧布局均不能为 nil")
	}
	if err := validateMigrations(oldLayout, newLayout, migrations); err != nil {
		return nil, err
	}

	report := &UpgradeReport{
		State:      StateLegalUpgrade,
		OldLayout:  oldLayout,
		NewLayout:  newLayout,
		Migrations: append([]Migration(nil), migrations...),
	}
	report.Findings = append(report.Findings, DetectSlotConflicts(oldLayout)...)
	report.Findings = append(report.Findings, DetectSlotConflicts(newLayout)...)
	report.Findings = append(report.Findings, CheckInheritance(oldLayout, newLayout)...)

	oldByPos := indexByPosition(oldLayout)
	newByPos := indexByPosition(newLayout)
	oldByName := indexByName(oldLayout)
	newByName := indexByName(newLayout)
	oldMaxSlot := oldLayout.maxUsedSlot()

	handledNewPositions := map[position]bool{}

	// 第一轮：遍历旧字段，识别同位置改动、字段移动与字段删除/槽位复用。
	for _, oldEntry := range oldLayout.Entries {
		pos := oldEntry.position()
		newEntry, samePosition := newByPos[pos]

		if samePosition && newEntry.Name == oldEntry.Name && newEntry.Type == oldEntry.Type {
			handledNewPositions[pos] = true
			continue
		}

		if relocated, moved := newByName[oldEntry.Name]; moved && (!samePosition || relocated.Type != oldEntry.Type) {
			handledNewPositions[relocated.position()] = true
			report.recordStructuralChange(
				oldEntry.Name,
				fmt.Sprintf("字段 %s 从槽位 %s 偏移 %d 移动到槽位 %s 偏移 %d",
					oldEntry.Name, oldEntry.Slot.Text(16), oldEntry.Offset,
					relocated.Slot.Text(16), relocated.Offset),
			)
			if !migrationCovers(migrations, MigrateMove, relocated.Name, oldEntry.Name) {
				report.addUnmigrated(oldEntry.Name, "字段移动需要 MOVE 显式迁移")
			} else {
				recordMigrationChange(&report.Changes, "moved", relocated.Name,
					findMigration(migrations, MigrateMove, relocated.Name, oldEntry.Name))
			}
			continue
		}

		if samePosition {
			report.recordStructuralChange(
				oldEntry.Name,
				fmt.Sprintf("槽位 %s 偏移 %d 由字段 %s(%s) 变为 %s(%s)",
					oldEntry.Slot.Text(16), oldEntry.Offset,
					oldEntry.Name, oldEntry.Type, newEntry.Name, newEntry.Type),
			)
			if !positionCoveredByMigration(migrations, newEntry, oldEntry) {
				report.addUnmigrated(newEntry.Name, "已使用槽位被复用，需要 CLEAR/INITIALIZE/MOVE 显式迁移")
			} else {
				report.Changes = append(report.Changes, Change{
					Kind:      "repurposed",
					Field:     newEntry.Name,
					Detail:    "同槽位字段替换，已由显式迁移覆盖",
					Migration: firstCoveringMigration(migrations, newEntry, oldEntry),
				})
			}
			handledNewPositions[pos] = true
			continue
		}

		// 旧槽位在新布局中完全空缺：字段被删除。
		report.recordStructuralChange(
			oldEntry.Name,
			fmt.Sprintf("字段 %s 被删除，槽位 %s 偏移 %d 腾空", oldEntry.Name, oldEntry.Slot.Text(16), oldEntry.Offset),
		)
		if !migrationCovers(migrations, MigrateClear, oldEntry.Name, "") {
			report.addUnmigrated(oldEntry.Name, "删除字段需要 CLEAR 显式迁移，避免旧值被新字段误读")
		} else {
			recordMigrationChange(&report.Changes, "removed", oldEntry.Name,
				findMigration(migrations, MigrateClear, oldEntry.Name, ""))
		}
	}

	// 第二轮：遍历新字段，识别槽位复用与新增（尾部追加或中间插入）。
	for _, newEntry := range newLayout.Entries {
		pos := newEntry.position()
		if handledNewPositions[pos] {
			continue
		}
		if _, existed := oldByName[newEntry.Name]; existed {
			continue // 重命名字段移动已在第一轮处理
		}
		if _, oldPosOccupied := oldByPos[pos]; oldPosOccupied {
			continue // 同位置复用已在第一轮处理
		}

		if newEntry.Slot.Cmp(oldMaxSlot) > 0 {
			report.Changes = append(report.Changes, Change{
				Kind:   "appended",
				Field:  newEntry.Name,
				Detail: "向未使用槽位尾部追加字段，兼容升级，无需迁移",
			})
			handledNewPositions[pos] = true
			continue
		}

		report.recordStructuralChange(
			newEntry.Name,
			fmt.Sprintf("新字段 %s 插入已使用槽位区间内的槽位 %s 偏移 %d",
				newEntry.Name, newEntry.Slot.Text(16), newEntry.Offset),
		)
		if !migrationCovers(migrations, MigrateInitialize, newEntry.Name, "") {
			report.addUnmigrated(newEntry.Name, "向已使用区间插入字段需要 INITIALIZE 显式迁移")
		} else {
			recordMigrationChange(&report.Changes, "inserted", newEntry.Name,
				findMigration(migrations, MigrateInitialize, newEntry.Name, ""))
		}
		handledNewPositions[pos] = true
	}

	report.finalize()
	return report, nil
}

// Apply 执行升级：先通过 Plan 核对，再按显式迁移搬移存储，最后切换 EIP-1967 实现槽位。
// 管理员槽位不在升级流程中修改，避免提权或管理员丢失。
func Apply(storage *ProxyStorage, oldLayout, newLayout *Layout, implementation Address, migrations []Migration) (*UpgradeReport, error) {
	if storage == nil {
		return nil, errors.New("netsec: 存储不能为 nil")
	}
	report, err := Plan(oldLayout, newLayout, migrations)
	if err != nil {
		return nil, err
	}
	if report.Blocked {
		return report, ErrUpgradeBlocked
	}

	oldByName := indexByName(oldLayout)
	newByName := indexByName(newLayout)
	for _, migration := range migrations {
		switch migration.Op {
		case MigrateMove:
			source := oldByName[migration.FromField]
			target := newByName[migration.Field]
			value := storage.GetField(source)
			if err := storage.SetField(target, value); err != nil {
				return report, fmt.Errorf("netsec: 迁移 %s -> %s 失败: %w", source.Name, target.Name, err)
			}
		case MigrateClear:
			entry, ok := oldByName[migration.Field]
			if !ok {
				entry = newByName[migration.Field]
			}
			if err := storage.SetField(entry, big.NewInt(0)); err != nil {
				return report, fmt.Errorf("netsec: 清空字段 %s 失败: %w", entry.Name, err)
			}
		case MigrateInitialize:
			entry := newByName[migration.Field]
			if err := storage.SetField(entry, migration.Value); err != nil {
				return report, fmt.Errorf("netsec: 初始化字段 %s 失败: %w", entry.Name, err)
			}
		default:
			return report, fmt.Errorf("netsec: 未知迁移类型 %q", migration.Op)
		}
	}

	storage.SetAddress(ImplementationSlot, implementation)
	return report, nil
}

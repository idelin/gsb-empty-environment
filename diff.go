package netsec

import (
	"fmt"
	"sort"
)

// statePriority 为阻断性发现提供稳定的排序与最终状态选择优先级。
var statePriority = map[State]int{
	StateSlotConflict:      0,
	StateLayoutMisaligned:  1,
	StateUnmigratedUpgrade: 2,
	StateLegalUpgrade:      3,
	StateReadbackVerified:  4,
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if statePriority[findings[i].State] != statePriority[findings[j].State] {
			return statePriority[findings[i].State] < statePriority[findings[j].State]
		}
		if findings[i].Entry != findings[j].Entry {
			return findings[i].Entry < findings[j].Entry
		}
		return findings[i].Message < findings[j].Message
	})
}

func indexByPosition(layout *Layout) map[position]Entry {
	out := make(map[position]Entry, len(layout.Entries))
	for _, entry := range layout.Entries {
		out[entry.position()] = entry
	}
	return out
}

func indexByName(layout *Layout) map[string]Entry {
	out := make(map[string]Entry, len(layout.Entries))
	for _, entry := range layout.Entries {
		out[entry.Name] = entry
	}
	return out
}

// recordStructuralChange 登记一条布局错位发现；同一字段只保留首条错位解释。
func (r *UpgradeReport) recordStructuralChange(entry, message string) {
	for _, finding := range r.Findings {
		if finding.State == StateLayoutMisaligned && finding.Entry == entry {
			return
		}
	}
	r.Findings = append(r.Findings, Finding{
		State:   StateLayoutMisaligned,
		Message: message,
		Entry:   entry,
	})
}

// addUnmigrated 登记一条未迁移发现。
func (r *UpgradeReport) addUnmigrated(entry, message string) {
	r.Findings = append(r.Findings, Finding{
		State:   StateUnmigratedUpgrade,
		Message: message,
		Entry:   entry,
	})
}

// finalize 汇总发现并确定最终状态：存在阻断发现时升级被阻断。
func (r *UpgradeReport) finalize() {
	sortFindings(r.Findings)
	r.Blocked = false
	r.State = StateLegalUpgrade
	hasMisalignment := false
	for _, finding := range r.Findings {
		if finding.State == StateLayoutMisaligned {
			hasMisalignment = true
		}
		if finding.Blocking() {
			r.Blocked = true
			if r.State == StateLegalUpgrade ||
				statePriority[finding.State] < statePriority[r.State] {
				r.State = finding.State
			}
		}
	}
	// 无阻断问题但存在已被迁移覆盖的错位时，升级仍合法；保留错位发现用于审计解释。
	if !r.Blocked && hasMisalignment {
		r.State = StateLegalUpgrade
	}
}

func migrationCovers(migrations []Migration, op MigrationOp, field, fromField string) bool {
	for _, migration := range migrations {
		if migration.Op != op || migration.Field != field {
			continue
		}
		if op == MigrateMove && migration.FromField != fromField {
			continue
		}
		return true
	}
	return false
}

func findMigration(migrations []Migration, op MigrationOp, field, fromField string) *Migration {
	for i := range migrations {
		migration := &migrations[i]
		if migration.Op != op || migration.Field != field {
			continue
		}
		if op == MigrateMove && migration.FromField != fromField {
			continue
		}
		return migration
	}
	return nil
}

func recordMigrationChange(changes *[]Change, kind, field string, migration *Migration) {
	*changes = append(*changes, Change{
		Kind:      kind,
		Field:     field,
		Detail:    "布局变更已由显式迁移覆盖",
		Migration: migration,
	})
}

// positionCoveredByMigration 判断复用旧槽位的新字段是否被任一合法迁移覆盖。
func positionCoveredByMigration(migrations []Migration, newEntry, oldEntry Entry) bool {
	for _, migration := range migrations {
		switch migration.Op {
		case MigrateClear:
			if migration.Field == oldEntry.Name || migration.Field == newEntry.Name {
				return true
			}
		case MigrateInitialize:
			if migration.Field == newEntry.Name {
				return true
			}
		case MigrateMove:
			if migration.Field == newEntry.Name || migration.FromField == oldEntry.Name {
				return true
			}
		}
	}
	return false
}

func firstCoveringMigration(migrations []Migration, newEntry, oldEntry Entry) *Migration {
	for i := range migrations {
		migration := &migrations[i]
		switch migration.Op {
		case MigrateClear:
			if migration.Field == oldEntry.Name || migration.Field == newEntry.Name {
				return migration
			}
		case MigrateInitialize:
			if migration.Field == newEntry.Name {
				return migration
			}
		case MigrateMove:
			if migration.Field == newEntry.Name || migration.FromField == oldEntry.Name {
				return migration
			}
		}
	}
	return nil
}

// validateMigrations 在核对前拒绝引用不存在字段、缺少初始值等无效迁移，
// 避免“声明了迁移”给出虚假的安全感。
func validateMigrations(oldLayout, newLayout *Layout, migrations []Migration) error {
	oldByName := indexByName(oldLayout)
	newByName := indexByName(newLayout)
	seen := map[string]bool{}
	for _, migration := range migrations {
		key := string(migration.Op) + ":" + migration.FromField + "->" + migration.Field
		if seen[key] {
			return fmt.Errorf("netsec: 重复迁移 %s", key)
		}
		seen[key] = true

		switch migration.Op {
		case MigrateMove:
			if _, ok := oldByName[migration.FromField]; !ok {
				return fmt.Errorf("netsec: MOVE 迁移源字段 %s 在旧布局中不存在", migration.FromField)
			}
			if _, ok := newByName[migration.Field]; !ok {
				return fmt.Errorf("netsec: MOVE 迁移目标字段 %s 在新布局中不存在", migration.Field)
			}
		case MigrateClear:
			if _, ok := oldByName[migration.Field]; !ok {
				if _, ok := newByName[migration.Field]; !ok {
					return fmt.Errorf("netsec: CLEAR 迁移字段 %s 在新旧布局中均不存在", migration.Field)
				}
			}
		case MigrateInitialize:
			entry, ok := newByName[migration.Field]
			if !ok {
				return fmt.Errorf("netsec: INITIALIZE 迁移字段 %s 在新布局中不存在", migration.Field)
			}
			if migration.Value == nil {
				return fmt.Errorf("netsec: INITIALIZE 迁移字段 %s 缺少初始值", migration.Field)
			}
			if migration.Value.Sign() < 0 || uint(len(migration.Value.Bytes())) > entry.Size {
				return fmt.Errorf("netsec: INITIALIZE 初始值超出字段 %s 的 %d 字节宽度", entry.Name, entry.Size)
			}
		default:
			return fmt.Errorf("netsec: 未知迁移类型 %q", migration.Op)
		}
	}
	return nil
}

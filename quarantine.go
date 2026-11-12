package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// ObjectID 是宿主文件系统分配给对象的稳定对象号（模型中对应 inode 号）。
type ObjectID uint64

// Generation 是对象代次。对象号可能在对象销毁后被复用，代次用于区分
// 复用前后承载不同内容的对象。
type Generation uint64

// ObjectRef 以“对象号 + 代次”唯一标识一个对象代次。
type ObjectRef struct {
	ID         ObjectID
	Generation Generation
}

// String 返回对象代次的可读表示。
func (r ObjectRef) String() string {
	return fmt.Sprintf("object(id=%d,gen=%d)", r.ID, r.Generation)
}

// DirEntry 描述完整引用图中的一个目录项（名字）。
type DirEntry struct {
	Parent ObjectID
	Name   string
	Object ObjectRef
}

// RefGraph 是宿主在某一时刻给出的完整目录引用图，覆盖普通目录树中
// 仍然生效的全部名字。
type RefGraph struct {
	Entries []DirEntry
}

// NamesOf 返回引用图中指向指定对象代次的全部名字，按父目录与名字排序，
// 保证事务处理顺序确定。
func (g RefGraph) NamesOf(target ObjectRef) []DirEntry {
	var out []DirEntry
	for _, entry := range g.Entries {
		if entry.Object == target {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Parent != out[j].Parent {
			return out[i].Parent < out[j].Parent
		}
		return out[i].Name < out[j].Name
	})
	return out
}

var (
	// ErrNameNotFound 表示目录项或父目录已不存在。
	ErrNameNotFound = errors.New("netsec: directory entry not found")
	// ErrNameMoved 表示目录项当前指向的对象代次与事务预期不符，
	// 典型场景是名字被重定向、替换，或对象号已被新代次复用。
	ErrNameMoved = errors.New("netsec: directory entry no longer points to the target object generation")
	// ErrQuarantineIncomplete 表示仍有名字指向目标对象代次，隔离事务未全部完成；
	// 此时拒绝执行门禁保持生效，可凭报告中的恢复清单重试或回滚。
	ErrQuarantineIncomplete = errors.New("netsec: quarantine incomplete, execution gate remains active")
)

// HostFS 是 Go 文件宿主必须实现的事务边界。实现需要保证各方法线性化：
// 门禁按对象代次精确生效，移动在校验对象代次后原子完成，从而消除
// “先查后移”之间的竞态窗口。
type HostFS interface {
	// Graph 返回当前的完整目录引用图。
	Graph() RefGraph

	// InstallGate 为指定对象代次安装“拒绝执行”门禁。
	InstallGate(ref ObjectRef) error
	// GateInstalled 报告指定对象代次的门禁是否仍在生效。
	GateInstalled(ref ObjectRef) bool
	// RemoveGate 撤销指定对象代次的门禁。
	RemoveGate(ref ObjectRef) error

	// MoveToQuarantine 原子地校验 (parent, name) 当前确实指向 ref，
	// 然后把该名字移入隔离区，返回可用于恢复的不透明隔离键。
	// 名字缺失返回 ErrNameNotFound；指向的对象代次不符返回 ErrNameMoved。
	MoveToQuarantine(ref ObjectRef, parent ObjectID, name string) (quarantineKey string, err error)
	// RestoreFromQuarantine 把隔离条目恢复为原目录项；目标位置已存在
	// 同名目录项时返回错误且不得覆盖。
	RestoreFromQuarantine(quarantineKey string, parent ObjectID, name string) error
}

// QuarantineRecord 记录一个已移入隔离区的名字及其恢复句柄。
type QuarantineRecord struct {
	Object        ObjectRef
	Parent        ObjectID
	Name          string
	QuarantineKey string
}

// QuarantineReport 是一次隔离事务的结果报告，同时充当恢复清单。
type QuarantineReport struct {
	// Target 是本次事务的目标对象代次。
	Target ObjectRef
	// GateOK 表示拒绝执行门禁当前是否生效。
	GateOK bool
	// Moved 是已经成功移入隔离区的全部名字。
	Moved []QuarantineRecord
	// Remaining 是事务结束时仍指向目标对象代次的名字（部分失败时非空）。
	Remaining []DirEntry
	// Err 为事务错误；nil 表示隔离完整完成。
	Err error
}

// Complete 报告目标对象代次是否已无任何名字残留在普通目录树中。
func (r *QuarantineReport) Complete() bool {
	return r.GateOK && r.Err == nil && len(r.Remaining) == 0
}

// maxQuarantineRounds 限制“移动 - 重新取图”的轮数，
// 防止目录项被持续搅动时事务无限循环。
const maxQuarantineRounds = 16

// Quarantine 执行隔离事务：先安装按对象代次拒绝执行的门禁，门禁成功后
// 再把引用图中指向该代次的所有名字逐个移入隔离映射。
//
// 门禁安装失败时不会移动任何名字；移动部分失败时门禁保留，报告的
// Remaining 给出恢复清单，可调用 Retry 继续隔离或 Rollback 整体回滚。
func Quarantine(host HostFS, graph RefGraph, target ObjectRef) (*QuarantineReport, error) {
	report := &QuarantineReport{Target: target}
	if err := host.InstallGate(target); err != nil {
		report.Err = fmt.Errorf("install execution gate for %s: %w", target, err)
		return report, report.Err
	}
	report.GateOK = true
	report.Moved, report.Remaining, report.Err = drainNames(host, graph, target, nil)
	return report, report.Err
}

// Retry 在门禁仍生效的前提下重新获取引用图，移走上次遗留的名字，
// 返回合并了历史隔离记录的新报告。
func (r *QuarantineReport) Retry(host HostFS) (*QuarantineReport, error) {
	next := &QuarantineReport{
		Target: r.Target,
		Moved:  append([]QuarantineRecord(nil), r.Moved...),
	}
	if !host.GateInstalled(r.Target) {
		if err := host.InstallGate(r.Target); err != nil {
			next.Err = fmt.Errorf("reinstall execution gate for %s: %w", r.Target, err)
			return next, next.Err
		}
	}
	next.GateOK = true
	next.Moved, next.Remaining, next.Err = drainNames(host, host.Graph(), r.Target, next.Moved)
	return next, next.Err
}

// Rollback 按恢复清单把所有隔离条目恢复回原目录项；仅在全部恢复成功后
// 才撤销门禁。任一恢复失败时返回聚合错误、保留门禁，并把未恢复的条目
// 留在 Moved 中供再次调用。
func (r *QuarantineReport) Rollback(host HostFS) error {
	failed := make([]QuarantineRecord, 0, len(r.Moved))
	var errs []error
	for _, record := range r.Moved {
		if err := host.RestoreFromQuarantine(record.QuarantineKey, record.Parent, record.Name); err != nil {
			errs = append(errs, fmt.Errorf("restore %q for %s: %w", record.QuarantineKey, record.Object, err))
			failed = append(failed, record)
		}
	}
	r.Moved = failed
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if err := host.RemoveGate(r.Target); err != nil {
		return fmt.Errorf("remove execution gate for %s: %w", r.Target, err)
	}
	r.GateOK = false
	r.Remaining = host.Graph().NamesOf(r.Target)
	r.Err = nil
	return nil
}

// drainNames 逐轮把指向 target 的名字移入隔离区。首轮使用调用方提供的
// 引用图，后续轮次向宿主重新取图，以发现期间新增的硬链接与重命名。
// 单轮没有任何成功移动时停止，避免对持续故障空转。
func drainNames(host HostFS, graph RefGraph, target ObjectRef, prior []QuarantineRecord) (moved []QuarantineRecord, remaining []DirEntry, err error) {
	moved = append(moved, prior...)
	current := graph
	for round := 0; round < maxQuarantineRounds; round++ {
		if round > 0 {
			current = host.Graph()
		}
		progress := false
		for _, entry := range current.NamesOf(target) {
			key, moveErr := host.MoveToQuarantine(target, entry.Parent, entry.Name)
			if moveErr != nil {
				// 名字缺失、被改名或已指向其他对象代次：靠下一轮刷新引用图收敛；
				// 注入类硬故障同样留待恢复清单处理。
				continue
			}
			moved = append(moved, QuarantineRecord{
				Object:        target,
				Parent:        entry.Parent,
				Name:          entry.Name,
				QuarantineKey: key,
			})
			progress = true
		}
		remaining = host.Graph().NamesOf(target)
		if len(remaining) == 0 {
			return moved, nil, nil
		}
		if !progress {
			break
		}
	}
	return moved, remaining, fmt.Errorf("%w: %d name(s) still reference %s",
		ErrQuarantineIncomplete, len(remaining), target)
}

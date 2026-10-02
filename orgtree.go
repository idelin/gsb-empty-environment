// Package orgtree 实现可追溯、可修订的员工组织架构时态树。
//
// 每条汇报关系（任职时间段）都带有两个时间维度：
//   - 业务时间 [ValidFrom, ValidTo)：该汇报关系在现实中生效的区间；
//   - 系统时间 [SysFrom, SysTo)：该记录在系统中被知悉（录入）的区间。
//
// 补录历史调岗事件时，新记录与现存（SysTo 为无穷大）记录的业务区间若重叠，
// 旧记录会被自动分裂：重叠部分被新记录覆盖，不重叠的头尾部分以当前系统时间
// 重新录入，从而保证任意 (业务时刻, 知悉时刻) 的查询都可回溯、可修订。
package orgtree

import (
	"fmt"
	"math"
	"sort"
)

// Infinity 表示开区间的无穷端点。
const Infinity = math.MaxInt64

// Record 是一条汇报关系记录：Employee 在业务时间 [ValidFrom, ValidTo) 内
// 汇报给 Manager，该事实在系统时间 [SysFrom, SysTo) 内为系统所知。
type Record struct {
	Employee  string
	Manager   string
	ValidFrom int
	ValidTo   int
	SysFrom   int
	SysTo     int
}

// covers 判断记录是否覆盖查询点 (validTime, sysTime)。
func (r Record) covers(validTime, sysTime int) bool {
	return r.ValidFrom <= validTime && validTime < r.ValidTo &&
		r.SysFrom <= sysTime && sysTime < r.SysTo
}

// overlaps 判断业务区间 [from, to) 是否与记录的业务区间重叠。
func (r Record) overlaps(from, to int) bool {
	return r.ValidFrom < to && from < r.ValidTo
}

// Tree 是双时态组织架构树，内部以追加式记录列表保存全部历史版本。
type Tree struct {
	records []*Record
}

// New 创建一棵空的组织架构树。
func New() *Tree {
	return &Tree{}
}

// Assign 在系统时间 sysTime 录入一条汇报关系：
// employee 在业务时间 [validFrom, validTo) 内汇报给 manager。
//
// 录入时自动分裂与现存记录重叠的业务区间：
//   - 现存记录被关闭（SysTo 设为 sysTime），保留历史可回溯；
//   - 现存记录不重叠的头/尾部分以 sysTime 为 SysFrom 重新录入；
//   - 新记录覆盖 [validFrom, validTo)。
func (t *Tree) Assign(employee, manager string, validFrom, validTo, sysTime int) error {
	if validFrom >= validTo {
		return fmt.Errorf("orgtree: 非法业务区间 [%d, %d)", validFrom, validTo)
	}
	if employee == manager {
		return fmt.Errorf("orgtree: 员工不能汇报给自己")
	}

	var kept []*Record
	var fragments []*Record
	for _, r := range t.records {
		// 只处理同一员工、当前系统可见（SysTo 为无穷）、业务区间重叠的记录。
		if r.Employee != employee || r.SysTo != Infinity || !r.overlaps(validFrom, validTo) {
			kept = append(kept, r)
			continue
		}
		// 关闭旧记录：它在 sysTime 之后不再为系统所知。
		closed := *r
		closed.SysTo = sysTime
		kept = append(kept, &closed)

		// 分裂：旧业务区间左端不重叠的部分重新录入。
		if r.ValidFrom < validFrom {
			fragments = append(fragments, &Record{
				Employee:  employee,
				Manager:   r.Manager,
				ValidFrom: r.ValidFrom,
				ValidTo:   validFrom,
				SysFrom:   sysTime,
				SysTo:     Infinity,
			})
		}
		// 分裂：旧业务区间右端不重叠的部分重新录入。
		if validTo < r.ValidTo {
			fragments = append(fragments, &Record{
				Employee:  employee,
				Manager:   r.Manager,
				ValidFrom: validTo,
				ValidTo:   r.ValidTo,
				SysFrom:   sysTime,
				SysTo:     Infinity,
			})
		}
	}

	t.records = append(kept, fragments...)
	t.records = append(t.records, &Record{
		Employee:  employee,
		Manager:   manager,
		ValidFrom: validFrom,
		ValidTo:   validTo,
		SysFrom:   sysTime,
		SysTo:     Infinity,
	})
	return nil
}

// ManagerAt 查询在系统知悉时刻 sysTime 看来，员工在业务时刻 validTime
// 的直接汇报对象。第二个返回值表示是否存在该时刻的汇报关系。
func (t *Tree) ManagerAt(employee string, validTime, sysTime int) (string, bool) {
	for _, r := range t.records {
		if r.Employee == employee && r.covers(validTime, sysTime) {
			return r.Manager, true
		}
	}
	return "", false
}

// ChainAt 查询在系统知悉时刻 sysTime 看来，员工在业务时刻 validTime 的
// 完整汇报链（从直接汇报对象一直到最顶层），员工自身不包含在结果中。
// 若检测到环，返回错误。
func (t *Tree) ChainAt(employee string, validTime, sysTime int) ([]string, error) {
	var chain []string
	visited := map[string]bool{employee: true}
	current := employee
	for {
		manager, ok := t.ManagerAt(current, validTime, sysTime)
		if !ok {
			return chain, nil
		}
		if visited[manager] {
			return nil, fmt.Errorf("orgtree: 检测到汇报环: %s", manager)
		}
		visited[manager] = true
		chain = append(chain, manager)
		current = manager
	}
}

// Records 返回全部记录（含已关闭的历史版本）的副本，按录入顺序排列，
// 仅用于审计与调试。
func (t *Tree) Records() []Record {
	out := make([]Record, len(t.records))
	for i, r := range t.records {
		out[i] = *r
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Employee != out[j].Employee {
			return out[i].Employee < out[j].Employee
		}
		if out[i].SysFrom != out[j].SysFrom {
			return out[i].SysFrom < out[j].SysFrom
		}
		return out[i].ValidFrom < out[j].ValidFrom
	})
	return out
}

// Package main 实现带时态约束的 RBAC 权限委托内核。
//
// 模型采用双时态（bitemporal）语义：
//   - 业务时间 (valid time)：授权 / 委托 / 收回在业务上生效的时间区间；
//   - 知悉时间 (transaction/audit time)：系统记录该事实的时间。
//
// 查询时同时给定业务时间 bt 与知悉时间 kt，
// 回答“在知悉 kt 时看来，用户在业务时刻 bt 是否实际持有某权限”。
package main

import (
	"fmt"
	"sort"
)

// Interval 表示半开区间 [From, To)。
type Interval struct {
	From, To int64
}

// Contains 判断业务时刻 t 是否落在区间内。
func (iv Interval) Contains(t int64) bool { return t >= iv.From && t < iv.To }

func (iv Interval) String() string { return fmt.Sprintf("[%d, %d)", iv.From, iv.To) }

// Assignment 是一条直接角色授权（委托链的源头）。
type Assignment struct {
	ID         int
	User       string
	Role       string
	Valid      Interval // 业务有效区间
	RecordedAt int64    // 知悉时间：系统何时记录该授权
}

// Delegation 是一条临时委托：Delegator 将角色委托给 Delegatee。
type Delegation struct {
	ID         int
	Delegator  string
	Delegatee  string
	Role       string
	Valid      Interval
	RecordedAt int64
}

// Revocation 是对某条授权/委托的提前收回。
// 自业务时刻 EffectiveAt 起目标记录失效；该收回在知悉时刻 RecordedAt 才为系统所知。
type Revocation struct {
	TargetID    int
	EffectiveAt int64
	RecordedAt  int64
}

// Resolver 是时态权限委托解析内核。
type Resolver struct {
	permissions map[string]map[string]bool // role -> permission set
	assignments []*Assignment
	delegations []*Delegation
	revocations []Revocation
	nextID      int
}

// NewResolver 创建一个空解析器。
func NewResolver() *Resolver {
	return &Resolver{permissions: make(map[string]map[string]bool), nextID: 1}
}

// DefineRole 定义角色及其权限集合。
func (r *Resolver) DefineRole(role string, perms ...string) {
	set := r.permissions[role]
	if set == nil {
		set = make(map[string]bool)
		r.permissions[role] = set
	}
	for _, p := range perms {
		set[p] = true
	}
}

// GrantRole 直接授予用户角色，返回记录 ID。
func (r *Resolver) GrantRole(user, role string, valid Interval, recordedAt int64) int {
	a := &Assignment{ID: r.nextID, User: user, Role: role, Valid: valid, RecordedAt: recordedAt}
	r.nextID++
	r.assignments = append(r.assignments, a)
	return a.ID
}

// Delegate 由 delegator 将角色委托给 delegatee。
// 委托成立的前提：委托人在委托起始业务时刻、按当前知悉时间实际持有该角色。
func (r *Resolver) Delegate(delegator, delegatee, role string, valid Interval, recordedAt int64) (int, error) {
	if !r.HoldsRole(delegator, role, valid.From, recordedAt) {
		return 0, fmt.Errorf("delegation rejected: %s does not hold role %s at business time %d (as of knowledge %d)",
			delegator, role, valid.From, recordedAt)
	}
	d := &Delegation{ID: r.nextID, Delegator: delegator, Delegatee: delegatee, Role: role, Valid: valid, RecordedAt: recordedAt}
	r.nextID++
	r.delegations = append(r.delegations, d)
	return d.ID, nil
}

// Revoke 提前收回一条授权或委托：自业务时刻 effectiveAt 起失效，
// 该事实在知悉时刻 knownAt 进入系统。
func (r *Resolver) Revoke(targetID int, effectiveAt, knownAt int64) error {
	if !r.exists(targetID) {
		return fmt.Errorf("revocation rejected: no grant/delegation with id %d", targetID)
	}
	r.revocations = append(r.revocations, Revocation{TargetID: targetID, EffectiveAt: effectiveAt, RecordedAt: knownAt})
	return nil
}

func (r *Resolver) exists(id int) bool {
	for _, a := range r.assignments {
		if a.ID == id {
			return true
		}
	}
	for _, d := range r.delegations {
		if d.ID == id {
			return true
		}
	}
	return false
}

// revoked 判断记录 id 在 (bt, kt) 下是否已被收回。
func (r *Resolver) revoked(id int, bt, kt int64) bool {
	for _, rev := range r.revocations {
		if rev.TargetID == id && rev.RecordedAt <= kt && rev.EffectiveAt <= bt {
			return true
		}
	}
	return false
}

// HoldsRole 判定用户在业务时间 bt、知悉时间 kt 下是否实际持有角色。
// 沿委托链向上解析：任一环节过期或被收回，整条链在该时刻失效。
func (r *Resolver) HoldsRole(user, role string, bt, kt int64) bool {
	return r.holdsRole(user, role, bt, kt, make(map[string]bool))
}

func (r *Resolver) holdsRole(user, role string, bt, kt int64, visiting map[string]bool) bool {
	key := user + "\x00" + role
	if visiting[key] {
		return false // 委托环，拒绝
	}
	visiting[key] = true
	defer delete(visiting, key)

	// 1) 直接授权
	for _, a := range r.assignments {
		if a.User == user && a.Role == role &&
			a.RecordedAt <= kt && a.Valid.Contains(bt) && !r.revoked(a.ID, bt, kt) {
			return true
		}
	}
	// 2) 委托链：存在一条在 (bt, kt) 下有效的委托，且委托人同一时刻持有该角色
	for _, d := range r.delegations {
		if d.Delegatee == user && d.Role == role &&
			d.RecordedAt <= kt && d.Valid.Contains(bt) && !r.revoked(d.ID, bt, kt) &&
			r.holdsRole(d.Delegator, role, bt, kt, visiting) {
			return true
		}
	}
	return false
}

// EffectivePermissions 返回用户在 (bt, kt) 下的实际有效权限集合（升序）。
func (r *Resolver) EffectivePermissions(user string, bt, kt int64) []string {
	set := make(map[string]bool)
	for role, perms := range r.permissions {
		if r.HoldsRole(user, role, bt, kt) {
			for p := range perms {
				set[p] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

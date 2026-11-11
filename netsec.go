package netsec

import "errors"

// Hash 是散列环上的一个位置，环按数值顺时针排列并在最大值处回绕。
type Hash uint64

// Digest 是区参数的摘要，用于区分同一区在不同参数下生成的记录。
type Digest [32]byte

// Record 是一条调用方已完成签名验证的节点记录。
type Record struct {
	Hash        Hash   // 节点在环上的位置
	Successor   Hash   // 顺时针方向下一个节点的位置
	ZoneVersion uint64 // 记录所属的区版本
	Params      Digest // 区参数摘要
	Empty       bool   // 为 true 时表示空区声明，此时 Hash 与 Successor 无效
}

var (
	ErrNoEvidence        = errors.New("netsec: 未提供任何记录")
	ErrMixedZoneVersion  = errors.New("netsec: 记录来自不同的区版本")
	ErrMixedParams       = errors.New("netsec: 记录的区参数摘要不一致")
	ErrEmptyConflict     = errors.New("netsec: 空区声明与节点记录相互矛盾")
	ErrConflictSuccessor = errors.New("netsec: 同一节点存在冲突的后继")
	ErrNotProven         = errors.New("netsec: 目标未被证明不存在")
)

// VerifyNonExistence 组合一组已验签的记录，证明 target 在区中不存在。
// 所有记录必须属于同一区版本且参数摘要一致，不允许拼接跨区证据。
// 返回 nil 表示不存在性成立，否则返回具体错误。
func VerifyNonExistence(target Hash, records []Record) error {
	if len(records) == 0 {
		return ErrNoEvidence
	}

	version := records[0].ZoneVersion
	params := records[0].Params
	for _, r := range records[1:] {
		if r.ZoneVersion != version {
			return ErrMixedZoneVersion
		}
		if r.Params != params {
			return ErrMixedParams
		}
	}

	empty := records[0].Empty
	for _, r := range records[1:] {
		if r.Empty != empty {
			return ErrEmptyConflict
		}
	}
	if empty {
		// 空区声明只能单独使用：整区无节点，任何目标都不存在。
		if len(records) != 1 {
			return ErrEmptyConflict
		}
		return nil
	}

	successors := make(map[Hash]Hash, len(records))
	for _, r := range records {
		if prev, ok := successors[r.Hash]; ok && prev != r.Successor {
			return ErrConflictSuccessor
		}
		successors[r.Hash] = r.Successor
	}

	for _, r := range records {
		if inOpenInterval(r.Hash, r.Successor, target) {
			return nil
		}
	}
	return ErrNotProven
}

// inOpenInterval 报告 t 是否严格落在从 h 顺时针到 succ 的开区间内。
// h == succ 表示单节点环，区间覆盖除 h 自身外的整个环；
// h > succ 为回绕区间，等价于 (h, max] 与 [0, succ) 两段之并。
// t 与任一端点重合都不算不存在。
func inOpenInterval(h, succ, t Hash) bool {
	if h == succ {
		return t != h
	}
	if h < succ {
		return h < t && t < succ
	}
	return t > h || t < succ
}

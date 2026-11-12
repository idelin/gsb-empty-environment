package netsec

import "errors"

// 核验过程中可能返回的哨兵错误，宿主可用 errors.Is 判定具体原因。
var (
	// ErrDAGInvalid 表示备份 DAG 结构非法（缺块、重复键、环或分支头缺失）。
	ErrDAGInvalid = errors.New("netsec: invalid backup DAG")
	// ErrProofInvalid 表示证明字段不完整或格式非法。
	ErrProofInvalid = errors.New("netsec: invalid proof")
	// ErrProofUnknownBlock 表示证明指向的块不在 DAG 中。
	ErrProofUnknownBlock = errors.New("netsec: proof references unknown block")
	// ErrProofUnknownBranch 表示分支作用域证明指向的分支不存在。
	ErrProofUnknownBranch = errors.New("netsec: proof references unknown branch")
	// ErrProofRejected 表示证明未通过宿主认证器校验。
	ErrProofRejected = errors.New("netsec: proof rejected by authenticator")
	// ErrProofConflict 表示同一 (作用域, 块) 出现互相冲突的证明。
	ErrProofConflict = errors.New("netsec: conflicting proofs for the same scope and block")
	// ErrAuthenticator 表示认证器自身故障，区别于签名无效的拒证。
	ErrAuthenticator = errors.New("netsec: authenticator failure")
)

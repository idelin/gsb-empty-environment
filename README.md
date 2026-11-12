# MLS群epoch密钥推进

被移除的群成员保留旧epoch密钥并重放旧Commit。为Go群消息服务实现MLS式epoch推进包，供Commit处理调用：epoch_secret由init_secret与commit_secret经HKDF-SHA256提取，GroupContext哈希绑定epoch、树哈希和确认转录，epoch须加一，被移除成员的叶不贡献commit_secret。用旧epoch重放与分叉Commit验证。

## 使用

`EpochState`保存当前 epoch 的 `init_secret`、树哈希、确认转录和活跃叶。服务收到
`Commit` 后调用 `ProcessCommit`；成功时状态原地推进，失败时状态保持不变。

```go
state := netsec.EpochState{
	GroupID:    []byte("group"),
	Epoch:      7,
	InitSecret: initSecret,
	TreeHash:   oldTreeHash,
	Leaves:     []netsec.LeafID{0, 1, 2},
}

commit := netsec.Commit{
	GroupID:                state.GroupID,
	Epoch:                  7, // Commit 所基于的 epoch
	ParentTreeHash:         state.TreeHash,
	ParentTranscriptHash:   netsec.ComputeTranscriptHash(state.ConfirmationTranscript),
	ParentCommitID:         state.LastCommitID,
	ParentGroupContextHash: state.LastGroupContextHash,
	TreeHash:               newTreeHash,
	ConfirmationTranscript: confirmationTranscript,
	RemovedLeaves:          []netsec.LeafID{2},
	Contributions: []netsec.LeafContribution{
		{LeafID: 0, Secret: leaf0Secret},
		{LeafID: 1, Secret: leaf1Secret},
	},
}

advance, err := netsec.ProcessCommit(&state, commit)
if err != nil {
	// errors.Is(err, netsec.ErrStaleEpoch) 或 ErrForkedCommit
}
// state.Epoch == 8；advance.EpochSecret 已绑定 GroupContext。
```

## 安全语义

- 提交的 `Epoch` 必须等于本地状态 epoch，旧提交重放返回 `ErrStaleEpoch`。
- 可选但推荐设置的父树哈希、父转录哈希、父 Commit ID 和父 GroupContext 哈希
  必须与本地状态一致，否则返回 `ErrForkedCommit`。
- 叶贡献按 `LeafID` 排序后聚合；位于 `RemovedLeaves` 中的叶即使仍发送旧密钥
  贡献也会被丢弃，不参与 `commit_secret`。
- 若 Commit 解析层已聚合好 `CommitSecret`，可只设置该字段（此时不能同时设置
  `Contributions`），但其聚合必须已排除被移除叶。
- 逐叶贡献模式下所有活跃叶都必须贡献；未知叶、重复叶、缺失贡献和空群组都会
  被拒绝。
- `epoch_secret = HKDF-Extract(init_secret, commit_secret)`；再以
  `GroupContextHash` 作为 HKDF info 派生绑定 epoch 的密钥。GroupContext 覆盖
  新 epoch、树哈希和确认转录。
- 成功后 `init_secret` 立即轮换为不可从旧 epoch 推导的新值。

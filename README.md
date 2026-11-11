# 门限访问结构份额规划

## shareplan 包

实现位于 `shareplan/`，在 GF(257) 上做递归 Shamir 拆分。

- `Node` 描述策略树：内部节点用 `Threshold` + `Children` 表示 t 取 n，叶子用 `Participant` 命名参与者；`Validate` 校验结构并限制最多 16 名不同参与者。
- `Split(root, secret, rng)` 递归拆分，返回 `Plan`；每个叶子出现位置都有独立 `LeafShare.ShareID`，同一参与者多次出现时各份额互不混用。
- `Recover` 自底向上恢复，且只在同一节点内插值；未知、重复、越界或跨节点搬迁的份额都会报错。
- `Combine(nodeID, shares)` 做节点级恢复并返回拉格朗日系数，来自其他节点的份额以 `ErrMixedNodes` 拒绝。
- `MinimalParticipantSets` 返回所有极小合格参与者集合，`ReconstructionFor` 返回某合格集合的恢复系数，含每叶子系数与按参与者聚合的系数。
- `VerifyRecovery` 穷举至多 2^16 个参与者子集，断言恢复成功当且仅当策略满足，并逐节点校验同节点组合。

```go
root := &shareplan.Node{Threshold: 2, Children: []*shareplan.Node{
	{Participant: "alice"},
	{Threshold: 2, Children: []*shareplan.Node{
		{Participant: "bob"},
		{Participant: "carol"},
	}},
}}
plan, _ := shareplan.Split(root, 42, nil)
secret, err := plan.Recover(plan.Leaves())
_, _ = secret, err
```

备份服务希望用嵌套门限策略控制恢复，而不是固定k取n。实现供Go备份宿主调用的份额规划包，策略树节点为t取n、叶子为参与者；在GF(257)上递归拆分秘密，叶子可在多处出现但每处份额独立标识，恢复只组合同节点的份额。返回最小参与者集合及恢复系数，最多16人。用所有参与者子集验证恢复当且仅当策略满足，拒绝跨节点混合份额。

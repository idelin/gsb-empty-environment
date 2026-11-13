# 同一密钥流下多条秘密的联合泄漏

加密日志误把多条消息放在同一异或密钥流上。单独看每条候选都合法，合起来却可能暴露秘密。本包对候选组合做联合约束求解，而不是只检查记录两两是否兼容。

## 输入

`Record` 包含记录 ID、密文、共享密钥流中的非负起始偏移，以及明文候选。最多 12 条记录，每条最多 64 个候选；候选长度必须等于对应密文长度。候选可以重复，密文可以为空，各记录的密钥流区间可以不重叠。

```go
records := []netsec.Record{
	{
		ID:         "a",
		Ciphertext: []byte{0, 0},
		Offset:     0,
		Candidates: [][]byte{{7, 7}, {8, 8}},
	},
	{
		ID:         "b",
		Ciphertext: []byte{0},
		Offset:     1,
		Candidates: [][]byte{{7}, {9}},
	},
}
result, err := netsec.Recover(records)
```

## 结果

- `Status` 为 `Satisfiable`、`Unsatisfiable` 或 `Unknown`。
- `Candidates[i]` 是记录 `i` 中至少出现在一个完整解里的候选索引。
- `CandidateStates` 区分已证可行、已证不可行和未解决的候选；`Unknown` 搜索不会把当前交集当成唯一候选。
- `Determined` 只包含所有可能完成都一致的密钥流字节，`DeterminedBytes` 是同一结果的有序形式。搜索不完整时按尚未排除的候选保守计算。
- `Witnesses` 是最多 20 个完整候选索引元组，按输入记录顺序的候选索引字典序排列。`Truncated` 仅在确实还存在更多解时为 true。
- `SearchNodes` 和 `BudgetExhausted` 说明有限搜索的消耗。

预算用 `Options.MaxSearchNodes` 控制；零使用默认值，`UnlimitedSearchNodes` 表示不设上限。也可调用 `RecoverWithBudget`。

## 冲突

无完整解时，`Conflict.RecordIDs`/`RecordIndices` 给出记录数最少的冲突子集，数量相同时按记录 ID、再按输入索引字典序决胜。`Conflict.Deletions` 对冲突子集中每条记录给出删除该记录后的见证；因为子集最小，冲突子集的每个真子集都可满足。若删除后整个输入也可满足，见证会扩展到其余全部记录并置 `CoversAllInput`；否则回退到冲突子集内的见证，其余记录为 -1。`ProvenUnsatisfiable` 在搜索预算不足但已证明无解时仍为 true。

## 验证

`recover_test.go` 使用小候选集的全组合枚举作为 oracle，逐项比较状态、每条记录的可行候选、确定字节、见证顺序和截断标志；随机实例覆盖偏移、重叠、非重叠、空消息、重复候选和整体冲突。另有三方两两兼容但整体无解、最大 12×64 输入、预算耗尽和冲突字典序决胜等回归测试。

运行：

```sh
go test ./...
go vet ./...
```

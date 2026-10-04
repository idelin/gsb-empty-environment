# bayes — 精确贝叶斯证据更新库

基于 `math/big.Rat` 的有理数精确后验计算，无浮点误差。

## 设计要点

- **证据组联合似然**：`AddEvidence(id, likelihood)` 直接接收整组观测在各
  假设下的联合似然 P(E|H)，库不拆分、不相乘组内观测，避免把相关观测
  误当独立事件。
- **精确后验**：`Posterior()` 按 P(h)·∏P(eᵢ|h) 归一化，结果为有理数。
- **撤销证据**：`RemoveEvidence(id)` 移除整组证据，后验随之恢复。
- **重复证据只计一次**：同一 id 重复加入为空操作，返回 `(false, nil)`。
- **矛盾检测**：加入证据后总似然为零时返回 `ErrContradiction`，状态不变。
- **候选观测选择**：`BestCandidate` 用联合概率枚举计算期望误判率
  Σₒ (P(o) − maxₕ P(h)P(o|h))，选最低者，平局按编号字典序取小。

## 示例

```go
e, _ := bayes.New(map[bayes.Hypothesis]*big.Rat{
    "H1": big.NewRat(1, 3), "H2": big.NewRat(2, 3),
})
e.AddEvidence("E1", map[bayes.Hypothesis]*big.Rat{
    "H1": big.NewRat(3, 4), "H2": big.NewRat(1, 8),
})
post := e.Posterior() // H1: 3/4, H2: 1/4
```

## 测试

```sh
go test ./...
```

测试包含联合概率枚举核对：多组证据的后验与独立实现的联合枚举
P(h)·∏P(eᵢ|h) 归一化结果逐一比对；期望误判率与逐结果归一化的暴力
枚举比对。

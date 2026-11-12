# 查询差分重建风险

统计接口允许用户组合多次计数，单次合法仍可能解出个体属性。给Go准入服务写可导入的线性泄漏判定包，查询为对至多二十个秘密位的整数线性方程，返回是否唯一确定任一未公开秘密位及其最小证明方程集合。用有理消元与0/1可行性共同判断，预算耗尽返回未知，不能把实数唯一性当作全部结论。

## 包：`linearleak`

导入路径 `example.org/netsec/q0436/linearleak`，入口为 `linearleak.Analyze`。

```go
rep, err := linearleak.Analyze(
    nBits,
    []linearleak.Equation{{Coeff: []int64{1, 1}, RHS: 0, Tag: "q7"}},
    nil, // 已公开的秘密位
    linearleak.Budget{}, // 零值使用默认预算
)
```

- `rep.Status`：`safe` / `leak` / `inconsistent` / `unknown`。
- `rep.Findings`：每个被唯一确定的未公开位，含强制值、最小证明方程下标与标签。
- `rep.UnknownBits`：预算耗尽而无法判定的位。
- 判定结合有理数高斯-若尔当消元与带预算的 0/1 可行性回溯；实数唯一性只用于
  标注 `Finding.RealFixed`，不作为最终结论来源。
- `Finding.ExactMin` 区分“已证明基数最小”与“包含极小核但预算不足以证明更小不存在”。

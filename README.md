# freqsketch

固定容量、可合并的近似频数摘要（Misra–Gries 风格，带显式误差界），
接收正整数计数更新。

## 保证

设摘要容量为 `k`，已处理总量为 `N = Total()`：

- 对被监控项：`Count <= 真实频数 <= Count + Err`，其中 `Err = MaxError()`。
- 对未出现在摘要中的项：`真实频数 <= MaxError()`。
- 任意时刻 `MaxError() <= N / k`。
- 合并保留已处理总量（`Total` 相加），且不丢局部误差：两侧各自的
  压缩误差全部累加进合并后的误差界。
- 未出现过的项永远不会被报告；只有当从未发生过压缩（摘要从未满）时，
  报告的误差界才为 0，此时所有计数都是精确的。

定容摘要无法把被驱逐的质量归属到具体项，因此误差界是全局的
（与 DataSketches ItemsSketch 的上下界语义一致）。

## 用法

```go
s := freqsketch.New[string](100) // 容量 100
s.Add("alice", 1)
s.Add("bob", 5) // 计数必须是正整数

other := freqsketch.New[string](100)
// ... 在另一台机器/分块上累积 ...
s.Merge(other) // 容量必须相同；Total 与误差界都被保留

for _, est := range s.Estimates() {
    // 真实频数 ∈ [est.Count, est.Count+est.Err]
}
count, err, ok := s.Lookup("alice") // ok=false 时真实频数 <= s.MaxError()
```

## 合并次序

合并不要求结合律意义上的逐位一致：不同分块大小与合并次序（顺序、
逆序、平衡树）产生的估计可以不同，但上述界限对任意次序都成立。
`TestMergeOrdersAndBlockings` 用独立完整计数对多种次序逐一验证。

## 测试

```sh
go test ./...
```

测试用独立的精确计数验证：所有报告界限、合并后总量守恒、
容量不被突破、`MaxError() <= Total()/Capacity()`、不同分块合并次序的
保证，以及未出现项不会被报成精确频数。

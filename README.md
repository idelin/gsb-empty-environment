# 秘密分区策略可组合性

不同插件各自只输出一部分秘密分类，组合后可能完整恢复秘密。给Go隐私宿主写分区分析包，秘密域有限，每个插件把秘密映射到公开标签；计算插件组合形成的公共细分，找达到指定识别率的最小插件集合，至多二十插件。输出每个交叉等价类和先验质量，用全子集与逐秘密枚举核对。

## 包结构（`package netsec`）

- `NewAnalysis(secrets, plugins, weights)`：校验有限秘密域、标签向量与先验（缺省均匀先验），插件数上限 `MaxPlugins = 20`。
- `Refine(mask)` / `RefineNames(names)`：计算所选插件的公共细分，返回每个交叉等价类的成员、先验质量 `Mass`、贝叶斯猜测目标及其概率。
- `MinimalSetIndices(threshold, metric)` / `MinimalSets(...)`：分支定界枚举所有达到识别率的最小基数插件集合；达不到时返回 `ErrUnreachable`。
- `GreedySet(...)`：每步加入提升最大的插件，给出一个可行上界集合。
- `Exhaustive()`：枚举全部 `2^n` 子集并记录两种识别率。
- `Verify()`：用三套独立实现交叉核对——键分组细分、逐秘密两两比较的并查集枚举、逐秘密等价类扫描，并校验贝叶斯率的加插件单调性。
- `FormatPartition` / `FormatVerifyReport`：输出等价类、先验质量与核对报告。

## 识别率定义

- `MetricBayes`（默认）：一次性猜测成功率，对每个等价类取类内最大先验后求和。
- `MetricSingleton`：被完全揭示的单例等价类的先验质量之和。

空字符串标签表示插件对该秘密弃权，按独立标签值处理；对所有秘密都弃权的插件不揭示任何信息。

## 快速示例

```go
analysis, _ := NewAnalysis(
    []string{"alice", "bob", "carol", "dave"},
    []Plugin{
        {Name: "region", Labels: []string{"east", "east", "west", "east"}},
        {Name: "device", Labels: []string{"phone", "laptop", "phone", "laptop"}},
    },
    nil, // 均匀先验
)

partition, _ := analysis.RefineNames([]string{"region", "device"})
fmt.Print(analysis.FormatPartition(partition))

sets, err := analysis.MinimalSets(0.75, MetricBayes)
fmt.Println(sets, err)

if report := analysis.Verify(); !report.OK() {
    panic(FormatVerifyReport(report))
}
```

运行测试与示例：

```sh
go test ./... -v
```

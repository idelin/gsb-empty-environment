大模型并行编译器在两个算子之间常需把多维张量从一种设备网格分片布局变换成另一种布局（如将按模型轴切分并带局部未归约和的张量，重排为按序列轴切分），直接全局 AllReduce 再切分往往远不如 ReduceScatter 接 AllToAll 省带宽。为并行编译器交付可导入的 Go 重分片搜索包。输入张量各维大小（≤4 维）、设备网格各轴大小与单位字节带宽权值（2 或 3 轴），源与目标布局（指定每个张量维绑定哪条网格轴、是否复制，以及哪些网格轴上当前持有待求和的 Partial 状态）。合法单步通信原语包括：沿轴 a 的 AllGather（取消某维按 a 切分，代价 (M_a-1)/M_a * 完整输出字节 * w_a）、LocalSlice（零通信代价按 a 切分某复制维）、AllToAll（把按轴 a 切分的维 d1 换成按轴 a 切分的维 d2，代价 (M_a-1)/M_a * 单卡字节 * w_a）、ReduceScatter（消去轴 a 的 Partial 并同时按 a 切分某复制维，代价 (M_a-1)/M_a * 输入字节 * w_a）与 AllReduce（消去轴 a 的 Partial 保持复制，代价 2*(M_a-1)/M_a * 单卡字节 * w_a）。求精确最小总通信代价的原语序列（精确有理数或放大整数比较，并列取步数最少，再按序列字典序最小）。验收用小状态图最短路对照最优代价与语义等价，覆盖 ReduceScatter 优于 AllReduce+LocalSlice、AllToAll 一步换维优于 AllGather+LocalSlice、先 LocalSlice 缩小字节再跨慢轴通信，以及非法非整除维切分拒绝。

## `reshard` 包

导入路径：`prescreen.local/ps-cf3ce4e29d/reshard`。

- 输入：`Problem{Shape, BytesPerElement, Mesh{Ranks, Weights}, Source, Target}`；张量 1–4 维，设备网格 2–3 轴，`Layout.Shard[d]` 为切分该张量维的网格轴（`-1` 表示复制），`Layout.Partial[a]` 标记网格轴上是否持有待求和的 Partial 状态。
- 原语：`AllGather`、`LocalSlice`、`AllToAll`（含跨网格轴换绑，字段 `A,B,D1,D2`）、`ReduceScatter`、`AllReduce`，全部在 `reshard/types.go` 中定义并带可读的 `String()`。
- 精确性：每个原语代价乘以 `lcm(M_1,...,M_R)` 放大为整数比较，`Solution.ScaledCost` 为放大整数，`Solution.Cost`（`*big.Rat`）为同一值的精确有理表示，全程无浮点。
- 排序：先比总通信代价，再比步数，最后按原语序列字典序（`AllGather < AllReduce < AllToAll < LocalSlice < ReduceScatter`，同原语按 `A,B,D1,D2`）。
- 非法输入（含维度不能被网格轴整除的切分）返回错误；搜索内部对所有新切分强制整除校验。

最小调用示例：

```go
sol, err := reshard.Solve(reshard.Problem{
    Shape:           []int64{4, 8},
    BytesPerElement: 1,
    Mesh:            reshard.Mesh{Ranks: []int64{2, 2}, Weights: []int64{1, 1}},
    Source:          reshard.Layout{Shard: []int{0, -1}, Partial: []bool{false, false}},
    Target:          reshard.Layout{Shard: []int{-1, 1}, Partial: []bool{false, false}},
})
// sol.Ops 为一步跨轴 AllToAll，sol.Cost == 8/1
```

测试覆盖四类验收场景，并以独立 Bellman-Ford 小状态图最短路与独立语义模拟器对拍（代价、可达性、终态布局语义等价）。运行：`go test ./...`。

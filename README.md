分布式执行器要把嵌套集合列分割给多个工作器。为 Arrow 风格但题内自定义的列表、结构、稠密联合和字典数组实现可导入的 Go 切片包。给出每层独立偏移基数、切片起点、有效位图与子数组，允许不同父层通过字典共享子段。父 NULL 可包含非空物理区间，但不能贡献逻辑值；空列表与 NULL 不同。查询给若干顶层行的算术级数及叶字段路径，输出最少的原缓冲区读取段和可直接还原逻辑列表边界的投影计划；可多读但不能泄露被遮蔽的数据（题内给每个缓冲区的禁止读取区间）。读取成本含一次启动费及每字节费，重叠和相邻读取可共享。不得展开 2^44 个等步长行或共享字典副本，规则段用周期表示。提供独立读取模拟器，小数组全解码对照，含父空遮蔽非法标签、多个父引用同子片且选择顺序反转的情况。

## 实现

可导入包位于 `nestedslice/`，模块路径为
`prescreen.local/ps-763423cf12/nestedslice`。

- `periodic.go`：算术级数 `AP`、周期集合 `PSet`、并集、仿射映射和受限展开；
  负步长保留字典选择顺序，超过 `MaxExpand` 的非规则选择返回
  `ErrIrregularTooLarge`。
- `descriptor.go`：无负载字节的数组/缓冲区描述符，支持列表、结构、稠密联合、
  字典和定宽原语，统一元素区间到字节区间的换算。
- `engine.go`、`helpers.go`、`plan.go`：逐层投影、每层独立切片起点/偏移基数、
  父 NULL 阴影传播、共享字典单实例规划，以及带启动费和每字节费的读段 DP 优化。
- `simulator.go`：独立段读取器、真值全解码 `DecodeOracle`、按计划重放的
  `DecodePlan` 和等价性断言；读取器拒绝任何未覆盖或命中禁止区间的字节。

入口 API：

- `BuildPlan(root *Node, q Query, opts Options) (*Plan, error)`
- `DecodeOracle(root *Node, store *Store, rows PSet) ([]DValue, error)`
- `DecodePlan(plan *Plan, root *Node, store *Store) ([]DValue, error)`

## 验证

```bash
go test ./...
go vet ./...
```

测试覆盖 2^44 等步长行的符号化传播、非规则巨型列表拒绝展开、父 NULL 遮蔽
（含 NULL 联合行的非法标签）、空列表与 NULL 区分、多个父层共享同一片字典且
选择顺序反转、禁止读取区间切分、相邻/重叠读段合并、叶字段路径裁剪，以及
模拟器全解码与计划重放的逐值对照。

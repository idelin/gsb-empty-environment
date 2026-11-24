虚拟 NVMe 迁移时主机封存了实际 zone 盘像和几个完成队列，但部分 Zone Append 的返回 LBA 没有留下。设备后台要恢复客户的完成环，不能按到达先后猜 LBA。

本仓库提供可导入的 Go 包 `nvmerecov`。它从空区开始，按提交队列顺序和跨队列屏障求解所有合法执行前缀，并对每个丢失回执给出精确可能结果。内容使用值加重复次数的压缩表示，长盘面和长重复串不会被展开，也不会为相同内容合并不同请求。

## API

```go
analysis, err := nvmerecov.Analyze(model, observation)
joint, err := nvmerecov.QueryJoint(model, observation, assumptions)
```

- `Analyze` 先判断全部观测能否由同一条合法前缀解释。若不能，`Analysis.Valid` 为 false。
- `Analysis.Missing` 按请求编号列出每个无回执请求：`OutcomeNotExecuted`，或按真实返回内容/ LBA 去重后的执行结果。每个结果带一条可独立重放的前缀。
- `Publishable` 仅在已执行结果唯一时为 true；`Deterministic` 表示包括“未执行”在内也只有一种结果。若可能结果不唯一，`Analysis.Witnesses` 给出两条不同的可重放前缀。
- `QueryJoint` 同时约束整批假定回执，返回同一条共享前缀；逐项可行的假设若不能同链成立会被拒绝。

## 数据模型

- `Command`：固定请求编号、操作、目标区。Append 使用 `Data`；Read 使用 `Addr` 和 `Count`；Reset 无载荷。
- `Queue.Commands`：同一队列按序执行。
- `Barrier{Before, After}`：队列下标 `Before` 及其后请求必须等待 `After` 中的前驱完成。
- `Command.After`：额外的逐请求前驱约束。
- `Zone{Capacity, Initial}`：容量最大可到 2^48 块；`Initial` 省略时从空区开始。
- `Blocks`：由 `Run{Empty, Value, Count}` 组成，`Count` 为 `uint64`，可跨 32 位。`Block.Empty` 是与“写入空字节值”不同的未写标记。
- `Observation`：断点时各区写指针、采样块、任意范围的盘像，以及带请求号的成功回执。Append 回执带 LBA，Read 回执带内容，Reset 只表示成功。

`Read` 结果和盘像通过真实字节内容比较；内部状态键也只做精确序列化，不使用哈希相等作为判定。

## 求解特性

- 每个请求至多执行一次，执行集合是各队列前缀并对屏障依赖闭合。
- Append 越界直接使该执行顺序不合法，不会换区。
- Reset 清空区内容并归零写指针，但已产生的回执仍保留在结果图中，因此“已返回但随后被 Reset 抹去”的效果不会被混淆。
- 只保留所有 Read、采样和盘像会观察到的地址区间；其余内容不进入状态，避免展开或枚举无意义排列。
- 状态合并使用精确内容键；相同追加内容不会合并请求编号，不同重放给出不同 LBA 时保留不同结果。

## 测试

`go test ./...` 覆盖：

- 两个相同 Append 交换执行顺序后的 LBA 歧义与两条独立前缀；
- 最终盘为空但旧 Read 回执能区分不同历史；
- 另一队列上的 Reset 被屏障约束；
- 长重复块串仅最后一块不同；
- 批量询问中逐项合法但联合不合法；
- 越界 Append 不换区、空标记与空字节值区分、超大重复次数与范围溢出。

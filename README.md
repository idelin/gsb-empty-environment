# 固件权限锁定位推导

硬件寄存器一旦锁定就不能再改权限，错误顺序可能永久留下可写敏感区。实现供Go配置宿主调用的规划包，寄存器为有限位字段，操作可改掩码但受锁定位与前置值限制，锁定位只能由零变一；求达到目标权限且始终不开放禁区的最短写入序列。最多24位可变状态，预算不足返回未完成，用小位域全搜索核对不可恢复死路。

## 模型

- 每个受保护区域占相邻两位：使能（可写权限）位 + 锁定位，最多 12 个区域（24 位可变状态）。
- 锁定位写 1 置 1、写 0 无效，置位后永不清零（write-one-to-set）。
- 区域未锁时可任意改写使能位；锁定后使能位冻结为锁定前的值，后续写使能被硬件忽略。
- 一次写事务为 `(WriteMask, Value)`；默认可同时改任意区域，`SingleField` 模式限制为单区域。
- `ForbiddenMask` 选中的使能位在**所有访问状态**（含起始态）必须为 0。

## 入口

- `PlanRegions(regions, singleField, budget)`：面向宿主的区域级规格，返回最短 `Plan`。
- `PlanModel(m, budget)`：原始位级模型，支持 `TargetMask` 表达 don't-care。
- `VerifyDeadEnds(m, maxStates)`：小位域全空间核对，前向 BFS/DFS 与反向 BFS/三色 DFS 交叉验证死路。
- `Simulate` 与 `Apply`：用同样的硬件语义回放事务，供宿主在自己的寄存器布局上复核。

## 状态

- `StatusSolved`：得到最短序列，`Plan.Writes` 即事务列表（起始即满足目标时为空）。
- `StatusUnreachable`：可证明无安全序列，例如锁定在错误使能值、起始态已开放禁区。
- `StatusUnfinished`：写入深度或访问状态数超过 `Budget`，问题尚未判定。
- `StatusInvalid`：模型或预算非法，原因见 `Plan.Err`。

## 预算

- `Budget.MaxWrites` 限制 BFS 深度，默认 24。
- `Budget.MaxVisited` 限制扩展的不同状态数，默认 `DefaultMaxVisited`。
- `Budget.DenseLimit` 以下的小状态空间使用紧凑父表，更大空间退化为哈希表，避免 24 位场景分配上百 MiB。

## 测试

- 随机对拍：独立的原始 `(mask, value)` 暴力 BFS 与规划器比对最短深度，并逐状态校验禁区不变量。
- 前驱逆关系：枚举全状态验证 `Predecessors` 与 `Apply` 语义完全一致。
- 全搜索一致性：三路算法对可达集合与死路集合必须一致。
- `go test ./...` 与 `go test -race ./...` 均通过。

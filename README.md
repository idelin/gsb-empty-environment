# shardplan

测试分片规划器：按预计运行时间把测试分配到固定数量的执行器。

## 目标（字典序）

1. **最少迁移**：在给定负载上限内，尽可能少地把测试迁离当前执行器；
2. **最小最大负载**：在所有最少迁移方案中，取最大负载最小者。

若负载上限内无可行方案，结果中的 `CapacityFloor` 给出容量下界——
任何方案所需的最小单执行器容量（即最优 makespan，精确计算）。

## 约束

- 最多 24 个测试（`MaxTests`）；
- `Group` 相同的测试必须分到同一台执行器（组内测试合并为调度单元）；
- `Executor = Unassigned(-1)` 表示新测试，不计入迁移数。

## 用法

```go
res, err := shardplan.Plan(tests, executors, loadLimit)
if err != nil { /* 参数非法 */ }
if !res.Feasible {
    // res.CapacityFloor 是可行所需的最小负载上限
}
// res.Assignment: 测试名 -> 执行器
// res.Migrations: 迁移数, res.MaxLoad: 最大负载
```

命令行（JSON 进，JSON 出；不可行时退出码为 1）：

```sh
echo '{"executors":2,"loadLimit":10,"tests":[
  {"name":"a","duration":6,"executor":0},
  {"name":"b","duration":4,"executor":0},
  {"name":"c","duration":4,"executor":1}]}' | go run ./cmd/shardplan
```

## 算法

同组测试合并为调度项后，按耗时降序做分支定界深度搜索：

- 目标按 (迁移数, 最大负载) 字典序比较；
- 迁移下界剪枝：每个未放置项在所有可行执行器上的最小迁移成本之和
  （负载只增不减，故可采纳）；
- 对称剪枝：仅当两个执行器负载相同**且**对所有剩余项迁移成本相同
  时才视为等价（执行器是有标签的）；
- 容量下界由独立的精确 makespan 搜索（LPT 初始解 + 分支定界）给出。

## 测试

```sh
go test ./...
```

包含与暴力枚举对拍的随机测试（2000 组），验证迁移数、最大负载与
容量下界的最优性。

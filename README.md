# matchtool — 带权二分图匹配（值班任务分配）

精确求解任务→员工分配，仅用 Go 标准库。目标按字典序依次优化：

1. **已分配任务数最大**（负收益边绝不预先丢弃，必要时照样使用）；
2. **总收益最大**（收益为有符号整数，费用流取负精确求解）；
3. **员工列表字典序最小**：按任务 ID 升序排列员工 ID，未分配任务用
   哨兵（= 所有 ID 最大值 + 1，大于一切 ID）表示，逐项比较取最小。

## 构建与测试

```sh
go build ./cmd/matchtool
go test ./match/
```

测试包含：孤立点、全负收益、多个等价最优（字典序决胜）、重复边冲突
（同一对保留最大收益）、删除当前匹配边、随机增删边与重新求解一致性，
以及 400 组 ≤7×7 随机实例与穷举参考（`BruteSolve`）逐位对拍，并对每个
结果运行独立校验器 `Verify`。

## JSON 命令行

```sh
matchtool solve  [-in file]   # 求解；可选 updates 逐条增删边，输出每步结果
matchtool brute  [-in file]   # 独立穷举复算（指数级，仅供小实例核对）
matchtool verify [-in file]   # 独立校验 {"instance":..., "result":...}
```

输入（solve / brute）：

```json
{
  "tasks": [1, 2, 3],
  "employees": [10, 20, 30],
  "edges": [{"task": 1, "employee": 10, "profit": -4}],
  "updates": [{"op": "remove", "task": 2, "employee": 30},
              {"op": "add", "task": 3, "employee": 10, "profit": 1}]
}
```

`solve` 输出 `{"initial": Result, "steps": [Result...]}`，每个 Result 含
`tasks`、`sentinel`、`numAssigned`、`totalProfit`、`employeeByTask`、
`assignment` 与 `certificate`。增删单条边后重新求解，结果与从零求解
完全一致（测试 `TestUpdatesConsistentWithFreshSolve` 保证）。

## 最优性证据（可被独立程序核对）

- **基数最大**：证书给出最小顶点覆盖（Kőnig 定理，由残量交错可达集
  构造）。校验只需集合逻辑：每条边至少一端在覆盖中，且
  |覆盖| = 已分配数 ⟹ 不存在更大的匹配。
- **收益最优**：证书给出残量网络的可行势（对偶变量）。对报告匹配
  对应的每条残量弧 (u,v) 检查 `pot[v] ≤ pot[u] + cost(u,v)` 全部成立
  ⟹ 无负费用残量环 ⟹ 该基数下费用最小（收益最大）。
- `matchtool verify` 是不调用求解器的独立代码路径；`matchtool brute`
  提供独立精确复算路径（含字典序决胜）。

## 算法与复杂度

记 T=任务数，E=员工数，M=边数，F=最大匹配基数，V=T+E+2，A=M+T+E。

| 阶段 | 方法 | 时间 |
|---|---|---|
| 最大基数 | Kuhn 增广路 | O(T·M) |
| 收益最优 | 逐最短路增广（Bellman-Ford，精确处理负费用） | O(F·V·A) |
| 字典序细化 | 逐任务可行性探测，每次一次费用流 | O((M+T)·F·V·A) |
| 证书校验 | 覆盖检查 + 残量弧简约费用检查 | O(V·A) |
| 穷举参考 | 枚举全部匹配 | Θ(Σₖ C(T,k)C(E,k)k!)，仅 ≤7×7 |

空间 O(V+A)。所有算术为 int64 精确运算；假设 |收益| 与总量远小于
2^60。字典序细化的正确性：前缀固定后，逐位取能保持 (基数, 收益)
 最优完成存在的最小员工 ID，这正是字典序最小解的贪心刻画。

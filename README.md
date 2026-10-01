# 预算化规则最小删除（rule_prune）

规则集合形成有向优先图：每条规则是一条有向边 `src -> dst`，携带正整数删除成本。
当图中存在有向环时，删除**总成本最小**的规则子集使剩余图无环；平局时依次比较
**删除条数最少**、**删除规则 ID 排序后的字典序最小**。求解器为精确分支限界搜索，
支持节点预算：预算耗尽时返回当前最好可行解与合法下界（及可独立验证的下界证书）。

仅使用 Python 3.11 标准库，无第三方依赖。所有示例数据均为合成数据。

## 命令行用法

```bash
# 求解（结果写 stdout，退出码 0=已证最优，2=预算耗尽返回可行解）
python3.11 -m rule_prune solve examples/three_cycle.json
python3.11 -m rule_prune solve examples/shared_edge.json --budget 1000 -o sol.json

# 独立验证（退出码 0=全部检查通过，1=拒绝）
python3.11 -m rule_prune verify examples/shared_edge.json sol.json

# 运行测试
python3.11 -m unittest discover -s tests -v
```

`solve` 参数：

- `input`：实例 JSON 文件路径，`-` 表示从 stdin 读取。
- `--budget N`：分支限界最多展开的搜索节点数（默认 100000）。
- `--time-limit S`：可选的墙钟秒数上限，超时同样提前停止。
- `-o/--output`：将解 JSON 写入文件而非 stdout。

## 输入格式

```json
{
  "rules": [
    {"id": "e1", "cost": 1, "src": "A", "dst": "B"}
  ]
}
```

- `id`：非空字符串，全实例唯一。
- `cost`：正整数（布尔值会被拒绝）。
- `src` / `dst`：非空字符串节点名；`src == dst` 表示自环，必须被删除。

## 输出格式

```json
{
  "status": "optimal",
  "deleted": ["e1"],
  "deleted_cost": 1,
  "topological_order": ["B", "C", "A"],
  "lower_bound": 1,
  "certified_lower_bound": 1,
  "lower_bound_certificate": [["e1", "e2", "e3"]],
  "stats": {"nodes_expanded": 1, "budget": 100000, "elapsed_seconds": 0.0001}
}
```

- `status`：`optimal`（搜索树已闭合，结果证最优）或 `feasible`
  （预算/时间耗尽，返回当前最好可行解，最优性未证明）。
- `deleted` / `deleted_cost`：被删除规则 ID（排序）与总成本。
- `topological_order`：剩余图的字典序 Kahn 拓扑序。
- `lower_bound`：全局下界。`optimal` 时等于 `deleted_cost`；`feasible` 时为
  所有未展开搜索节点下界的最小值（沿任一分支单调，故为合法全局下界）。
- `certified_lower_bound` / `lower_bound_certificate`：可独立验证的下界证书——
  一组**边不相交**的有向环（用规则 ID 表示），下界为每个环上最小边成本之和。
  当 `certified_lower_bound == deleted_cost` 时，验证器可独立确认最优性。

## 验证器检查项

`verify` 不复用搜索逻辑，逐项重查：删除 ID 存在且唯一；成本一致；剩余图无环；
拓扑序包含全部节点且满足所有剩余边；证书中每个环是原图中真实的有向环、
环两两边不相交、证书下界与重算一致且不超过删除成本；全局下界满足
`certified_lower_bound <= lower_bound <= deleted_cost`。

## 算法说明

- 目标等价于最小权反馈边集（NP-hard），故采用精确分支限界：任找一个有向环，
  对环上每条边分支"删除它"，配合下界剪枝与平局规则剪枝。
- 下界为边不相交环 packing：每个环至少删一条边，边不相交的环必须删**不同**的边，
  因此各环最小边成本之和不超过最优值。共享边只被计入一个环，不会被逐环重复计费。
- 搜索前先用"逐环删最便宜边"的贪心得到可行 incumbent，保证预算为 0 时也有可行解。

## 验收边界对应

- 三环边成本 1/2/3：只删成本 1 的边（`examples/three_cycle.json`，
  测试 `ThreeCycleTest`）。
- 共享边参与两个环：删共享边一次即可，下界不逐环重复计费
  （`examples/shared_edge.json`，测试 `SharedEdgeTest`）。

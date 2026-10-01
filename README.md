# 最小差异发布选择器 (release_select)

在**互斥变更**、**依赖变更**（依赖环强制整体选取）与**整数资源容量**约束下，
精确求解**性能收益最大**的变更集合；平局时变更数量最少，再平局时按排序后 ID 字典序最小。
仅使用 Python 3.11 标准库。所有示例数据均为合成数据。

## 运行

```bash
python3.11 release_select.py examples/boundary.json            # 求解 + 穷举核验
python3.11 release_select.py examples/mixed.json --compact     # 紧凑输出
cat examples/boundary.json | python3.11 release_select.py -    # 标准输入
python3.11 release_select.py in.json --no-verify               # 跳过核验(报告将标注"未验证")
python3.11 release_select.py in.json --verify-limit 24         # 调整穷举单元数上限(默认 20)
```

退出码：`0` 正常；`2` 输入非法；`3` 求解器与穷举核验器结果不一致（不应发生）。

## 输入格式（JSON）

```json
{
  "capacity": 10,
  "changes": [
    {"id": "A", "benefit": 5,  "cost": 2, "deps": ["B"]},
    {"id": "B", "benefit": -2, "cost": 2},
    {"id": "C", "benefit": 4,  "cost": 3, "mutex": ["A"]}
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `capacity` | 非负整数 | 资源容量，选中变更的 `cost` 总和不得超过它 |
| `changes[].id` | 非空字符串，唯一 | 变更 ID |
| `changes[].benefit` | 整数（可为负） | 性能收益 |
| `changes[].cost` | 非负整数 | 资源消耗 |
| `changes[].deps` | 字符串数组，可省 | 直接依赖；选取时传递闭包必须同选 |
| `changes[].mutex` | 字符串数组，可省 | 互斥关系（对称，单边声明即可） |

语义要点：

- **依赖环不报错**：环上的变更构成强连通分量，必须**整体选取或整体放弃**，
  其收益/成本为成员之和（见输出 `groups`，`forced_together: true`）。
- 依赖环内部若存在互斥对，则该组**不可选取**（`internal_mutex_conflict: true`）。
- 空集恒可行（收益 0），因此结果收益不会为负。

## 输出格式（JSON）

- `selection`：`ids`（排序后选中 ID）、`benefit`、`cost`、`change_count`。
- `groups`：依赖环缩点后的单元列表（成员、合计收益/成本、是否强制同选、是否被选中）。
- `evidence`：约束证据——
  - `capacity`：`used` 与 `capacity` 的对照及 `satisfied`；
  - `dependencies`：每个选中变更的每条依赖是否被满足；
  - `mutex`：每个互斥对是否两边同选（`both_selected`）及是否满足；
  - `all_constraints_satisfied`：上述全部满足的汇总。
- `verification`：精确穷举核验器结论——
  - `verified: true` 时给出 `enumerated_sets`（枚举子集总数 2^n）、`feasible_sets`、
    穷举最优 `best`，以及 `matches_solver` / `optimality_proven`；
  - `verified: false` 时给出 `reason`（单元数超过 `--verify-limit`，或使用
    `--no-verify` 跳过），此时**最优性未验证**，输出如实标注，不谎称最优。

## 方法

1. Tarjan 强连通分量把依赖图缩成单元 DAG；单元收益/成本为成员之和。
2. 预计算每个单元的依赖闭包，剔除闭包内含互斥冲突或成本超容量的单元。
3. 分支限界精确搜索（上界 = 当前收益 + 剩余单元正收益和），
   按 `(收益大, 变更数少, ID 字典序小)` 比较。
4. 穷举核验器独立枚举全部 `2^n` 个单元子集，逐一判定可行性并取最优，与求解器对照。
   求解与核验均为指数级复杂度，适用于中小规模实例；超出核验上限时明确报告未验证。

## 测试

```bash
python3.11 -m unittest test_release_select -v
```

覆盖：验收边界（A 收益 5 依赖 B 收益 -2，容量允许时整体收益 3，且不得单选 A 报 5）、
容量阻断依赖、互斥、依赖环强制同选/整体放弃、环内互斥、传递依赖、两级平局规则、
穷举核验一致性、核验上限如实报告、输入校验与 CLI 行为。

# 依赖快照许可求交规划器

为多个依赖同时选择满足各自许可表达式（`AND` / `OR` / 括号）的最小成本许可集合。
重复许可只计一次成本；声明为不兼容的许可对不得同时入选。求解是精确的（穷举每个
表达式的最小满足集合并对其笛卡尔积求交），因此报告的成本可证最优；最坏复杂度随
输入规模指数增长，适用于依赖快照级别的合成数据规模。

## 运行

```sh
python3 license_planner.py input.json   # 退出码 0=optimal, 1=infeasible, 2=用法错误
python3 -m unittest -v                  # 运行测试
```

## 输入格式（JSON）

```json
{
  "licenses":     {"X": {"cost": 1}, "Y": {"cost": 2}},
  "incompatible": [["Y", "Z"]],
  "dependencies": {"A": "X OR Y", "B": "Y AND Z"}
}
```

- `licenses`：许可名 → 非负整数合规成本。
- `incompatible`：不兼容许可对列表，可省略。
- `dependencies`：依赖名 → 许可表达式。`AND` 优先级高于 `OR`，支持括号，
  关键字大小写不敏感。

## 输出格式（JSON）

最优时：

```json
{
  "status": "optimal",
  "total_cost": 2,
  "selected": ["Y"],
  "witness": {"A": {"or": {"license": "Y", "selected": true}}, "B": {...}}
}
```

`witness` 是每棵表达式的见证路径：`or` 只保留被选中的分支，`and` 保留两个子树，
叶子记录所选许可。

不可行时输出冲突证书：

```json
{
  "status": "infeasible",
  "certificate": {
    "reason": "...",
    "options_per_dependency": {"A": [["X"], ["Y"]], "B": [["Y", "Z"]]},
    "incompatible_pairs": [["Y", "Z"]],
    "blocking_combinations": [{"combination": {...}, "conflicts": [["Y", "Z"]]}]
  }
}
```

`blocking_combinations` 逐一列出每种跨依赖选项组合被哪对不兼容许可阻断，合起来
构成不可行的穷举证书。

## 示例

```sh
python3 license_planner.py examples/shared_license.json       # 选 Y，总成本 2
python3 license_planner.py examples/infeasible_conflict.json  # Y 与 Z 不兼容，不可行
```

所有示例数据均为合成数据。

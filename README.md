# 审计抽样覆盖优化

在预算约束下选择检查项，使风险场景得到充分覆盖。每个风险场景要求至少
`k` 个来自**不同检查家族**的证据；同一家族的多个检查只计 1 个家族，
不能虚增覆盖次数。

## 运行

```bash
python3 audit_coverage.py examples/sample_input.json      # 从文件读取
cat examples/sample_input.json | python3 audit_coverage.py # 从标准输入读取
python3 audit_coverage.py input.json -o result.json        # 输出到文件
python3 -m unittest test_audit_coverage -v                 # 运行测试
```

仅依赖 Python 3.11+ 标准库，无第三方包。

## 输入格式（JSON）

```json
{
  "budget": 20,               // 必填，成本预算上限（非负数）
  "hours_budget": 30,         // 可选，总执行工时上限（非负数）
  "scenarios": [              // 风险场景列表，id 唯一
    {"id": "R1", "weight": 10, "k": 2}
  ],
  "checks": [                 // 检查项列表，id 唯一
    {"id": "chk-a", "family": "访谈", "cost": 4, "hours": 5,
     "covers": ["R1"]}
  ]
}
```

- `scenarios[].weight`：场景权重（非负，缺省 1）；`k`：所需不同家族证据数（≥1，缺省 1）。
- `checks[].family`：检查家族；`cost`/`hours` 非负（缺省 0）；`covers` 为覆盖的场景 id 列表。
- `covers` 引用未知场景、id 重复、负值等不合法输入会被拒绝并以退出码 2 报错。

## 输出格式（JSON）

```json
{
  "objective": {"covered_weight": 20, "total_cost": 14, "total_hours": 18},
  "selected_checks": ["chk-b", "chk-c", "chk-d"],
  "scenarios": [
    {"id": "R1", "k_required": 2, "covered": true,
     "family_count": 2, "families": ["日志分析", "访谈"]}
  ]
}
```

- `selected_checks`：选中的检查 id（升序）。
- `scenarios[].families`：该场景实际获得的证据家族集合（去重、排序）；
  `covered` 当且仅当 `family_count >= k_required`。

## 优化目标（字典序）

1. 最大化已充分覆盖场景的权重之和；
2. 在前者相同下，最小化所选检查总成本；
3. 再相同下，取选中检查 id 排序后字典序最小者。

## 求解方法与最优性

求解器为带剪枝的分支定界穷举（`solve()`）：枚举检查子集，仅在
"剩余检查全部选入也无法达到更优权重"或"成本已不可能更优"时剪枝，
剪枝不改变最优性，因此对任意有限输入返回**可证明的最优解**。
`OptimalityCrossCheckTest` 另用独立的纯暴力枚举在 60 个随机实例上
逐一交叉验证了权重、成本、选中 id 三元组完全一致。

注意：该问题为 NP-hard（集合覆盖推广），精确求解适合中小规模
（检查数约 ≤ 25~30）；更大规模需改用近似/启发式，本组件未提供。

示例数据（`examples/sample_input.json`）均为合成数据。

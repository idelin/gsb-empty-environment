# 跨日班次最小人员覆盖（shift_cover）

在一周循环（`7 * 24 * 60 = 10080` 分钟）的时间轴上，给定分钟级需求区间与
候选班次（可跨周末、最长一天），为每个班次选择非负整数人数，使所有时段
需求被满足，并依次最小化：

1. **总成本**；
2. **总人数**；
3. **按班次 ID 排序的人数向量**（字典序最小）。

仅使用 Python 3.11 标准库。算法为分支限界：下界来自覆盖线性规划的对偶
可行解（`y_i = min cost_j / |S_j|`，对任意班次恒有 `sum y_i <= cost_j`），
因此是合法下界。支持节点数 / 时间预算中断；中断时返回当前最优可行解
（上界）与合法下界，**只有搜索完整结束（或未展开子树下界严格大于上界）
时才声明 `optimal`**。

## 运行

```bash
python3 shift_cover.py input.json            # 从文件读取
cat input.json | python3 shift_cover.py      # 从标准输入读取
python3 shift_cover.py input.json --node-limit 1000 --time-limit 5
python3 -m unittest discover -v              # 运行测试
```

## 输入格式（JSON）

```json
{
  "demands": [{"start": 600, "end": 660, "required": 2}],
  "shifts":  [{"id": "A", "start": 600, "end": 660, "cost": 3}],
  "node_limit": 100000,
  "time_limit_seconds": 10
}
```

- `start` / `end`：`[0, 10080)` 内的整数分钟，半开区间 `[start, end)`；
  `end <= start` 表示跨周末（回绕）。`start == end` 非法。
- `required`：该区间同时需要的非负整数人数；重叠区间需求取最大值。
- `shifts[].id`：非空字符串，全局唯一。
- `shifts[].cost`：非负整数。
- 班次时长不得超过 1440 分钟（一天），需求区间时长不限。
- `node_limit` / `time_limit_seconds` 可选，命令行参数优先。

## 输出格式（JSON）

| 字段 | 含义 |
| --- | --- |
| `status` | `optimal` / `feasible` / `no_solution` / `infeasible` |
| `proven_optimal` | 是否已证明最优（未证明时绝不置真） |
| `total_cost` / `total_staff` | 当前最优可行解的成本与人数 |
| `assignment` | `{班次ID: 人数}`（仅列出人数 > 0 的班次） |
| `lower_bound` | 合法下界（对偶可行解给出，中断时为未展开节点下界最小值） |
| `upper_bound` | 当前最优可行解成本（上界） |
| `nodes` / `elapsed_seconds` | 搜索节点数与耗时 |
| `message` | 结论说明；预算中断时明确标注“最优性未证明” |

退出码：`optimal` / `feasible` 为 0，`infeasible` / `no_solution` 为 1，
输入非法为 2。

## 示例

见 `example_input.json`（合成数据）：

```bash
python3 shift_cover.py example_input.json
```

需求 2 人，单人班成本 3 与覆盖相同区间成本 5 的班并存时，最优解为两个
成本 3 的班，总成本 6（对应验收边界）。

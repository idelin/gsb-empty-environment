# Track Scheduler

单操作连续占用调度器：为一个操作在兼容轨道集合中安排开始时刻，占用区间
包含前后安全间隔，需避开维护禁用窗与既有占用。纯 Python 3.11 标准库
（`fractions.Fraction` 精确有理数），不调用任何求解器。

## 语义约定

- 占用区间（半开）：`[start - safety_before, start + duration + safety_after)`。
  安全间隔同样占用容量，参与全部冲突判定。
- 维护窗与既有占用均为半开区间 `[a, b)`；占用区间与其边界相接
  （`occupied_end == a` 或 `b == occupied_start`）不算冲突。
- 兼容轨道列表为空（`"compatible_tracks": []`）时直接判 `INFEASIBLE`。
- 所有时间量为整数或有理数（JSON 中写 `3`、`"3/2"`），内部以 `Fraction`
  精确运算，无浮点误差。

## 状态分离

| 状态 | 含义 |
|---|---|
| `OPTIMAL` | 找到最早可行开始时刻，且已证明在所有兼容轨道上最小；若给预算则不超预算 |
| `FEASIBLE` | 找到最早可行开始时刻，但超出给定预算（预算未满足） |
| `BUDGET_UNDECIDED` | 未给 horizon、仅给预算时，预算内无可行槽；预算之外未搜索，可行性未决 |
| `INFEASIBLE` | 完整 horizon 内无可行占用区间，或兼容轨道为空 |

## 冲突证据

结果中的 `evidence` 与 `track_scans[].conflicts` 记录每次试探性占用区间
撞到的禁用区间（类型 `maintenance` / `occupancy`、标签、双方区间），
以及 `horizon` 阻断（最早占用区间超出 horizon 端点）和 `later_track`
（其它轨道无法提供更早占用起点）。这些证据构成最早性的证明链。

## 输入格式（JSON）

```json
{
  "operation": {"name": "op", "duration": "3/2", "safety_before": "1/2", "safety_after": 1},
  "horizon": {"start": 0, "end": 24},
  "budget": 8,
  "tracks": [
    {"id": "T1",
     "maintenance": [["0", "5"], {"start": "9", "end": "11", "label": "inspect"}],
     "occupied": [["6", "7"]]}
  ],
  "compatible_tracks": ["T1"]
}
```

`horizon`、`budget`、`compatible_tracks`、`maintenance`、`occupied` 均可省略；
`compatible_tracks` 省略表示全部轨道兼容，显式空数组表示直接不可行。

## 运行

```sh
python3 cli.py examples/optimal.json     # 单个场景，JSON 输出
./run_samples.sh                          # 跑全部样例
python3 -m unittest discover -s tests -v  # 测试
```

## 样例运行结果（摘要）

- `examples/optimal.json` → `OPTIMAL`，轨道 `T2`，`start = 5/2`，
  占用 `[2, 5)`；证据含 T2 撞上维护窗 `[0, 2)` 及 T1 为 `later_track`
  （其最早占用起点为 11）。
- `examples/feasible_over_budget.json` → `FEASIBLE`，`start = 8`，
  超预算 5（`budget_met = false`）。
- `examples/budget_undecided.json` → `BUDGET_UNDECIDED`，预算 10 内
  无槽（维护窗 `[0, 100)`），预算外未探索。
- `examples/infeasible.json` → `INFEASIBLE`，horizon `[0, 10)` 内容纳不下
  占用长度 8，证据含维护窗、既有占用与 horizon 阻断。
- `examples/no_compatible_tracks.json` → `INFEASIBLE`，
  原因 `no compatible tracks`。

## 测试结果

```
Ran 19 tests in 0.001s
OK
```

覆盖：有理数解析、半开相接不冲突、安全间隔占容量、维护窗推挤与证据、
空兼容轨道立即不可行、最早轨道选择与并列按 id 定序、预算内最优、
超预算可行、预算界定搜索未中判未决、horizon 不可行及阻断证据。

# Track Scheduler

连续占用指定兼容轨道的调度工具：为操作安排开始时刻，占用区间包含前后安全间隔，
避开维护禁用窗与既有占用，并在不可行时输出冲突证据。纯 Python 3.11 标准库实现，
所有数值用 `fractions.Fraction` 精确表示（整数或有理数），不调用任何求解器。

## 语义约定

- **半开区间**：所有区间为 `[start, end)`。操作边界与维护窗相接（`end == 窗.start`
  或 `start == 窗.end`）不算冲突。
- **安全间隔占容量**：操作实际占用 `[start - pre_buffer, end + post_buffer)`；
  既有 booking 也可携带自身 buffer，同样占用容量。
- **最优**：最早开始时刻；并列时按轨道 id 字典序取确定性结果。
- **三类结果分离**：`feasible`（可行性）、`optimal`（最优放置）、
  `budget_status`（`met` / `violated` / 未给预算时 `undecided`）分别输出。
- **空兼容轨道集**：`tracks` 为空或缺失时直接不可行。
- **冲突证据**：不可行时逐轨道给出合并后的禁用区间（含来源标签）、
  各空闲间隙长度与所需预留长度（时长 + 前后安全间隔）的对比。

## 输入格式（JSON）

```json
{
  "operation": {"id": "op1", "duration": "3", "pre_buffer": "1/2", "post_buffer": "1/2"},
  "horizon": {"start": 0, "end": 24},
  "budget": "20",
  "tracks": [
    {"id": "T1",
     "maintenance": [["6", "9"]],
     "bookings": [{"start": "2", "end": "4", "pre_buffer": 0, "post_buffer": 0}]}
  ]
}
```

- 数字可写整数、小数或 `"p/q"` 有理数字符串；`horizon.end` 可为 `null`（无界）。
- `budget` 为允许的最晚操作结束时刻（`start + duration`），可省略。

## 运行

```bash
python3 scheduler.py examples/feasible.json     # 或 cat inst.json | python3 scheduler.py
```

退出码：可行 `0`，不可行 `1`，输入非法 `2`。

## 运行样例

`examples/optimal_budget.json`（有理数、含预算，输出最优 + `budget_status: met`）：

```json
{
  "operation": "op-budget",
  "feasible": true,
  "optimal": {"track": "TB", "start": "5/2", "end": "5",
              "reserved_start": "2", "reserved_end": "11/2"},
  "budget": "6",
  "budget_status": "met",
  "candidates": [
    {"track": "TB", "start": "5/2", "end": "5", "reserved_start": "2", "reserved_end": "11/2"},
    {"track": "TA", "start": "7/2", "end": "6", "reserved_start": "3", "reserved_end": "13/2"}
  ],
  "evidence": null
}
```

`examples/infeasible_maintenance.json`（不可行，输出逐轨道冲突证据，节选）：

```json
{
  "feasible": false,
  "optimal": null,
  "budget_status": "undecided",
  "evidence": {
    "reason": "no compatible track has a free gap fitting the operation",
    "required_reserved_length": "6",
    "tracks": [
      {"track": "T1",
       "forbidden": [
         {"interval": ["3", "7"], "sources": ["maintenance[3,7)"]},
         {"interval": ["8", "11"], "sources": ["booking[8,10)+buffers[8,11)"]}
       ],
       "gaps": [
         {"interval": ["0", "3"], "length": "3", "fits": false},
         {"interval": ["7", "8"], "length": "1", "fits": false},
         {"interval": ["11", "12"], "length": "1", "fits": false}
       ],
       "reason": "no free gap fits reserved length 6 (duration + safety buffers)"},
      {"track": "T2",
       "forbidden": [{"interval": ["0", "12"],
                      "sources": ["maintenance[0,5)", "maintenance[5,12)"]}],
       "gaps": []}
    ]
  }
}
```

`examples/infeasible_empty_tracks.json`（空兼容轨道集，直接不可行）：

```json
{
  "feasible": false,
  "optimal": null,
  "budget_status": "undecided",
  "evidence": {
    "reason": "no compatible tracks: the compatible track set is empty",
    "required_reserved_length": "4",
    "tracks": []
  }
}
```

其余样例：`examples/feasible.json`（多轨道选最早）、
`examples/budget_undecided.json`（未给预算，`budget_status: undecided`）。

## 测试

```bash
python3 -m unittest -v
```

结果：32 个测试全部通过，覆盖半开边界相接、安全间隔占容量、有理数精确运算、
空轨道集、预算 met/violated/undecided、冲突证据内容、相邻维护窗合并、
视界裁剪与无界视界、输入校验及 CLI 退出码。

```
..................................................
----------------------------------------------------------------------
Ran 32 tests in 0.004s

OK
```

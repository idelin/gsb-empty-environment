# parking-planner

预约规划器：为车辆预约分配车位，求**接纳收益最大**的预约子集。

## 规则

- **占用时间含缓冲**：每单实际占用为左闭右开区间
  `[start - entry_buffer, end + exit_buffer)`。缓冲可按单覆盖，否则用全局默认值。
- **左闭右开**：两单占用区间仅在正长度相交时冲突；首尾相接
  （`a.end == b.start`）**不冲突**，可复用同一车位。
- **车型限制**：每单只能停入接受其车型的车位。
- **已确认预约不可撤回**：`confirmed: true` 的单必须全部被接纳；
  若互相冲突导致不可行，输出 `infeasible` 及冲突证据。
- **规模**：最多 25 单（超出报错）。求解为精确分支定界，
  上界为剩余收益后缀和；同收益时偏好接纳单数更多的方案。

## 用法

```sh
go build -o parking-planner .
./parking-planner -input examples/sample.json   # 或省略 -input 从 stdin 读
go test ./...
```

退出码：`0` 最优解；`2` 已确认预约不可行；`1` 输入错误。

## 输入（JSON）

```json
{
  "defaults": {"entry_buffer": 5, "exit_buffer": 5},
  "spots": [{"id": "P1", "vehicle_types": ["sedan", "suv"]}],
  "reservations": [{
    "id": "R1", "vehicle_type": "sedan",
    "start": 480, "end": 540,
    "entry_buffer": 10, "exit_buffer": 0,
    "profit": 120, "confirmed": true
  }]
}
```

- 时间为任意同一时间轴上的整数（如分钟）。
- `entry_buffer` / `exit_buffer` 可省略，缺省取 `defaults`。

## 输出（JSON）

- `status`: `optimal` | `infeasible`
- `total_profit`: 最大总收益
- `accepted[]`: 每单的 `spot` 与占用窗口 `occupy_from`/`occupy_to`（含缓冲）
- `rejected[]`: 每单的拒绝原因及**冲突证据** `conflicts[]` ——
  与其占用区间相交、且所停车位兼容该车型的已接纳预约
  （含对方车位与重叠区间）
- 不可行时：`note` 说明原因，`rejected[0].conflicts` 给出
  互相挤占的已确认预约证据

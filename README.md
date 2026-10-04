# parking-planner 预约规划器

为车辆预约分配车位，求**接纳收益最大的预约子集**，并输出车位安排与被拒单的冲突证据。

## 规则

- **占用区间**：每单预约对车位的占用为 `[arrival - entry_buffer, departure + exit_buffer)`，
  即入场前、离场后各加一段缓冲，缓冲时长全局配置（分钟）。
- **左闭右开**：两区间仅当真正相交（交集非空）才冲突；首尾相接
  （`A.end == B.start`）**不算**冲突，可停同一车位。
- **车型限制**：每个车位声明可接纳的车型列表，预约只能停入接纳其车型的车位。
- **已确认预约**：`confirmed: true` 的预约不可撤回，必须全部接纳；
  若已确认集合本身不可行（互相冲突且车位不足），求解报错并给出冲突诊断。
- **规模约束**：单次最多 25 单，超出即报错。

## 用法

```sh
go build -o parking-planner .
./parking-planner examples/sample.json   # 或 cat input.json | ./parking-planner
```

## 输入格式（JSON）

```json
{
  "entry_buffer_minutes": 10,
  "exit_buffer_minutes": 10,
  "spots": [{"id": "P1", "vehicle_types": ["sedan", "suv"]}],
  "reservations": [
    {"id": "R1", "vehicle_type": "sedan", "arrival": 480, "departure": 600,
     "revenue": 50, "confirmed": true}
  ]
}
```

时间统一为整数分钟（相对同一参考时刻）。

## 输出格式（JSON）

- `total_revenue`：最优总收益。
- `accepted`：每单被接纳预约的车位、占用区间（含缓冲，左闭右开）与收益。
- `rejected`：每单被拒预约的原因；因优化被拒时附 `conflicts` 冲突证据——
  在其每个可用车位上，列出与之时段相交的已接纳预约及交叠区间。
- `schedule`：每个车位的占用时间表（按开始时刻升序）。

## 算法

预约按收益降序做**分支限界搜索**：每单分支出"停入某可用车位"与"回绝"
（已确认预约无回绝分支），以"当前收益 + 剩余全部收益"为上界剪枝。
25 单规模下可快速求得精确最优解。正确性由随机小案例与暴力枚举对拍验证
（`go test ./...`）。

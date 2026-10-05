# reworksched — 检验失败返工排程器

在依赖图上排程：输入完整检验失败结果后，把指定返工链嵌入依赖图，
返工实例与原工序**共用资源池**，求**最短完工时间（makespan 最优）**的计划。
工序实例总数（含返工）上限为 **15**。

## 运行

```sh
go build -o reworksched .
./reworksched input.json      # 或 cat input.json | ./reworksched
go test ./...
```

## 输入格式

```json
{
  "resources": { "machine": 2, "technician": 1 },
  "jobs": [
    { "id": "cut",  "duration": 3, "demands": { "machine": 1 }, "deps": [] },
    { "id": "weld", "duration": 4, "demands": { "machine": 1, "technician": 1 }, "deps": ["cut"] },
    { "id": "paint","duration": 2, "demands": { "technician": 1 }, "deps": ["weld"] }
  ],
  "failures": [
    { "job": "weld", "rework": [
      { "id": "reweld",    "duration": 2, "demands": { "machine": 1 } },
      { "id": "reinspect", "duration": 1, "demands": { "technician": 1 } }
    ]}
  ]
}
```

- 时间均为非负整数，`duration` 为正整数；资源需求不超过资源池容量。
- `failures` 给出完整失败结果：每个失败工序对应一条**指定返工链**。
- 返工链展开规则：失败工序 `J` 生成实例 `J#<step>`，链按
  `J -> J#s1 -> J#s2 ...` 串接；`J` 的原后继改挂到链末端。
- 每个实例保留独立身份：`id` 全局唯一，`origin` 指向来源工序，
  返工实例绝不覆盖原工序实例。

## 输出格式

- `makespan` / `optimal`：最短完工时间（分支定界精确求解，`optimal` 恒为 true）。
- `schedule`：每个实例的 `start`/`end` 与身份（`id`/`origin`/`rework`）。
- `resource_intervals`：按资源池分组的占用区间（实例、起止、占用量）。
- `verification`：独立核验报告——
  - `identity_ok`：实例 ID 唯一、返工来源工序未被覆盖、实例数 ≤ 15；
  - `dependencies_ok` + `dependency_checks`：逐条依赖边核验 `前驱完工 <= 后继开工`；
  - `resources_ok` + `resource_violations`：扫描线核验任意时刻资源池不超用。

核验任何一项不通过时进程以退出码 2 结束。

## 求解方法

在“决策时刻选择可行启动子集”的枚举树上做分支定界：枚举覆盖含主动空闲
在内的全部可行调度，配合关键链/资源工作量下界剪枝与状态记忆化支配，
保证得到最优解；15 实例规模实测毫秒级完成。

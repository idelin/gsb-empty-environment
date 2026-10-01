# cacheplan — 有限缓存重计算规划

有向无环计算图（DAG）的重计算规划器。每个节点带整数计算成本 `cost` 与结果大小
`size`；执行节点要求其**全部父结果驻留内存**，且结果本身也要放得下；内存上限
`memory_limit` 恒定。允许零成本驱逐缓存结果并在之后付费重算。目标：以**最小总
计算成本**得到指定输出（规划结束时所有 target 同时驻留内存），并输出每步内存
占用与依赖证据。

小图上使用**精确状态搜索**：以"当前缓存的结果集合"为状态做 Dijkstra 搜索
（驱逐为零成本边，计算为正成本边），因此返回的计划是**可证明最优**的。
可选的扩展预算（expansion budget）用于中断搜索；中断时返回**已认证的 lower
bound**（Dijkstra  frontier 的最小费用，最优解一定 ≥ 该值）。

仅依赖 Python 3.11 标准库。

## 运行

```bash
python3.11 cacheplan.py solve SPEC.json [--budget N]   # 求最优计划，JSON 输出到 stdout
python3.11 cacheplan.py verify SPEC.json PLAN.json     # 回放校验计划（依赖/内存证据）
python3.11 -m unittest -v                              # 运行测试
```

退出码：`solve` 找到最优解为 0，infeasible/interrupted 为 1，输入非法为 2；
`verify` 校验通过为 0，否则为 1。

## 输入格式（SPEC.json）

```json
{
  "memory_limit": 7,
  "nodes": [
    {"id": "s",  "cost": 5, "size": 3, "parents": []},
    {"id": "x",  "cost": 1, "size": 3, "parents": ["s"]}
  ],
  "targets": ["t1", "t2"]
}
```

- `memory_limit`：非负整数，内存上限（单位与 `size` 一致）。
- `nodes[].id`：唯一字符串；`cost`/`size`：非负整数；`parents` 可省略，默认为 `[]`。
- 图必须无环；`targets` 必须引用已定义节点。

## 输出格式（solve）

```json
{
  "status": "optimal | infeasible | interrupted",
  "total_cost": 14,
  "lower_bound": 14,
  "expansions": 120,
  "steps": [
    {"step": 1, "action": "compute", "node": "s", "cost": 5,
     "parents": [], "parents_resident": true,
     "memory_after": 3, "cache_after": ["s"]},
    {"step": 2, "action": "evict", "node": "x",
     "memory_after": 2, "cache_after": ["t1"]}
  ]
}
```

- `status=optimal`：`total_cost` 为可证明的最小总计算成本，`lower_bound == total_cost`。
- `status=infeasible`：含 `reason`；若因"节点 + 父结果超过上限"则附 `violations`
  明细（规划器绝不在计算中隐式丢弃父结果来凑内存）。
- `status=interrupted`：预算耗尽；`total_cost` 为 null，`lower_bound` 为已认证下界，
  附 `bound_note` 说明。
- 每个 `compute` 步骤携带依赖证据：`parents` 列表、`parents_resident` 标志、
  该步之后的 `memory_after` 与完整 `cache_after` 快照。

`verify` 子命令输出 `{"ok": bool, "total_cost": int|null, "error": str|null}`，
逐步重放检查：父结果驻留、内存不超上限、声明的 `memory_after` 与实际一致、
结束时全部 target 驻留。

## 示例（均为合成数据）

```bash
python3.11 cacheplan.py solve examples/shared_parent.json   # 共享父节点复用，最优 9
python3.11 cacheplan.py solve examples/recompute.json       # 内存紧张迫使 s 重算，最优 14
python3.11 cacheplan.py solve examples/infeasible.json      # 单节点+父结果超限，不可行
python3.11 cacheplan.py solve examples/recompute.json --budget 3   # 预算中断，保留下界
```

## 模型假设

- 目标定义为规划结束时所有 `targets` **同时驻留**内存。
- 驱逐零成本、可在任意时刻发生；重算需重新支付完整 `cost`。
- 计算节点 v 的瞬时内存 = 当前缓存总量 + `size(v)`，其中必须包含 v 的全部父结果。
- 与 targets 无关的节点即使自身不可计算也不影响可行性（永远不会被执行）。

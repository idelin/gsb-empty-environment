# DAG 构建调度器

读取本地 JSON 描述的 DAG（每个节点带 CPU、内存需求与不可抢占时长），
在单机 CPU/内存容量约束下生成**确定性启发式**调度计划，并输出工期下界。
仅使用 Python 3.11 标准库，不联网解析任何包。

## 运行

```bash
python3.11 -m scheduler sample/build.json
```

输出为 JSON：`makespan`（启发式工期）、`lower_bound`（下界）、`gap` 以及
每个任务的起止时间。注意：启发式结果**不保证全局最优**，`gap` 只是相对
下界的差距上界。

## 输入格式

```json
{
  "version": "1.4.0-rc.1",
  "capacity": {"cpu": 4, "memory": 8},
  "packages": [{"name": "zlib", "version": "1.2.13"}],
  "tasks": [
    {"id": "compile", "cpu": 2, "memory": 4, "duration": 4, "deps": ["parse"]}
  ]
}
```

- `version` 与 `packages[].version` 为 `major.minor.patch`，可带 `-prerelease`
  预发布标识（如 `3.0.0-beta2`），仅本地解析校验。
- `cpu` / `memory` / `duration` 为不小于 1 的整数；`deps` 为前置任务 id 列表。
- 任务图必须无环；单个任务需求超过容量时直接判定不可行
  （输出 `status: infeasible`）。
- 可同时运行但资源合计超限的任务不会被排在同一时段（调度时按容量检查）。

## 方法

- 启发式：并行表调度（parallel SGS）。每个事件时刻按固定优先级
  （关键路径位次降序、时长降序、id 字典序）贪心启动可容纳的就绪任务，
  同一输入必得同一结果。
- 下界：max(关键路径长度, ⌈CPU 总工作量 / CPU 容量⌉, ⌈内存总工作量 / 内存容量⌉)。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

24 个用例覆盖：版本解析/排序、调度合法性（依赖与容量）、确定性、
内存/CPU 互斥、单任务超容量不可行、下界正确性与输入校验。

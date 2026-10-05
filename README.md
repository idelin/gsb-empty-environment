# reworksched — 检验失败返工链排程器

在依赖图上排程工序；输入完整检验结果后，失败工序触发指定返工链，
返工实例与原工序**共用同一资源池**，求解**最短完工时间（makespan）**的精确计划。

## 特性

- 返工链展开：失败工序 `T` 后插入 `T -> R1 -> ... -> Rn`，原后继改接到链尾 `Rn`
- 身份保留：每个工序实例（含返工）都有唯一 ID，返工绝不覆盖原工序；ID 冲突直接报错
- 精确求解：分支定界（决策点子集枚举 + 刻意闲置分支 + 关键路径下界），
  展开后实例数上限 15，输出带“已证明最短”标记
- 完整核验：逐条依赖约束核验 + 资源池容量逐区间核验

## 用法

```sh
go build -o reworksched .
./reworksched -input examples/workshop.json          # 文本报告
./reworksched -input examples/workshop.json -json    # JSON 报告
go test ./...
```

## 输入格式

```json
{
  "resources": { "machine": 2, "inspector": 1 },
  "tasks": [
    {
      "id": "mill", "duration": 4,
      "resources": { "machine": 1 },
      "predecessors": ["cut"],
      "inspection": "fail",
      "rework": [
        { "id": "mill-repair",  "duration": 2, "resources": { "machine": 1 } },
        { "id": "mill-recheck", "duration": 1, "resources": { "inspector": 1 } }
      ]
    }
  ]
}
```

- `resources`：资源池容量（所有工序与返工共用）
- `inspection`：`pass` / `fail`（缺省表示无需检验）；`fail` 必须给出非空 `rework` 链
- 校验：工期为正、需求不超过容量、无环、无重复 ID、展开后实例数 ≤ 15

## 输出

排程区间、每种资源的占用区间（`resource_usage`）、逐条依赖核验
（`dependency_checks`）以及 `dependencies_ok` / `resources_ok` 总判定。

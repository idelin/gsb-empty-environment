# 容量约束证据打包（evidence-packer）

在容量约束下选择最高价值的证据文件交付包。每个证据文件有大小、价值、
依赖文件集合和互斥版本组；交付包必须依赖闭包完整、每组至多一个文件、
总大小不超过容量。重复依赖只计一次大小（交付包是文件集合，共享依赖
自然只计一次）。仅使用 Python 3.11 标准库。

## 运行

```sh
python3.11 evidence_packer.py INPUT.json [--budget N] [--compact]
```

- `INPUT.json`：输入文件路径，`-` 表示从标准输入读取。
- `--budget N`：搜索节点预算。预算内穷竭搜索空间则结果**已证最优**；
  预算耗尽则输出**最佳已知**并在结果与 stderr 中明确标注未证最优。
- 退出码：0 正常；1 独立可行性复核未通过；2 输入/参数错误。

## 输入格式

```json
{
  "capacity": 10,
  "files": [
    {"id": "core", "size": 3, "value": 8, "deps": ["lib"]},
    {"id": "lib",  "size": 2, "value": 1},
    {"id": "plugin_v2", "size": 3, "value": 6, "deps": ["lib"], "group": "plugin"}
  ]
}
```

- `capacity`：非负整数。
- `files[].id`：非空字符串，全局唯一。
- `files[].size`：非负整数；`files[].value`：整数（可为负）。
- `files[].deps`：可选，字符串数组，引用的 id 必须存在；允许循环依赖
  （环上文件同进同出）。
- `files[].group`：可选，字符串；同组文件互斥，至多选一个。

## 优化目标与平局裁决

1. 总价值最大；
2. 平局取总大小更小者；
3. 再平局取排序后文件 ID 列表字典序更小者。

空交付包恒可行，因此任何实例都有解。

## 输出格式

```json
{
  "status": "optimal | best_known",
  "proven_optimal": true,
  "total_value": 17,
  "total_size": 9,
  "capacity": 10,
  "files": ["core", "docs", "lib", "plugin_v2"],
  "dependency_topology": {"core": ["lib"], "docs": [], "lib": [], "plugin_v2": ["lib"]},
  "verification": {"feasible": true, "problems": []},
  "search": {"nodes_explored": 23, "node_budget": null, "budget_exhausted": false}
}
```

- `files`：交付文件清单（字典序排序）。
- `dependency_topology`：交付包内每个文件的直接依赖列表。
- `verification`：独立可行性复核结果。复核函数 `verify_selection`
  不复用求解器任何中间状态，从零重算闭包完整性、互斥冲突与容量约束；
  `problems` 非空时进程以退出码 1 结束并告警。
- `proven_optimal` / `status`：区分**已证最优**（`optimal`）与预算耗尽
  下的**最佳已知**（`best_known`）。

## 算法

分支定界精确搜索：按价值密度降序逐个文件决策；纳入分支连带整个依赖
闭包（保证闭包完整，共享依赖只计一次大小），排除分支仅排除该文件本身
（其依赖仍可被其他文件独立纳入）。上界取"当前价值 + 剩余正价值之和"，
仅严格小于已知最优值时剪枝，保证大小与字典序平局裁决的正确性。

## 测试

```sh
python3.11 -m unittest test_evidence_packer -v
```

覆盖验收边界（容量 4 不可选 a；容量 5 选 a+b 总大小 5）、传递闭包、
共享依赖去重、互斥组、平局裁决、节点预算、独立复核与输入校验。
所有示例与测试数据均为合成数据。

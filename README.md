# tablefix — 表约束修复器（仅删除行）

给定若干张表、唯一键、外键与用户指定的行间冲突，在**只允许删除行**的前提下，
求使**删除权重总和最小**的修复方案，并输出**删除原因闭包**与**保留表核验报告**。

## 设计要点

- **全局联合求解，而非分治拼接**：唯一键、外键、指定冲突被统一编译为
  「互斥对」与「蕴含（子行保留 ⇒ 父行候选集非空保留）」两类约束，
  交给同一个分支定界搜索求全局最优。绝不会对每条约束单独修复后再合并局部解——
  局部最优的简单拼接可能不是全局最优（见 `TestGlobalOptimumBeatsLocalMerge`：
  逐条修复会删 12 的权重，全局最优只删 10）。
- **外键级联计入目标**：删除父行会迫使删除引用它的子行，级联代价在搜索中
  通过增量可行性剪枝被完整计入，而非事后补救。
- **规模上限 25 行**：总行数超过 25 直接报错；位掩码 + 后缀和上界剪枝使精确搜索足够快。
- **SQL 语义**：唯一键或外键列取 NULL（或缺失）的行不参与相应约束检查。

## 用法

```sh
go run . -input example.json      # 或 cat example.json | go run .
go test ./...
```

输入格式见 `example.json`：每张表声明 `unique_keys`（列名数组的数组），
每行有 `id`、可选 `weight`（缺省 1）与 `values`；`foreign_keys` 声明
子表列到父表列的引用；`conflicts` 声明不得同时保留的行对及原因。

## 输出

- `total_deleted_weight` / `deleted`：每个被删行的权重、原因类别
  （`unique_key_violation` / `declared_row_conflict` / `foreign_key_cascade` /
  `dangling_foreign_key` / `weight_tradeoff`）、`caused_by`（级联来源）与
  `chain`（从根本原因到该行的完整传导闭包）。
- `kept`：保留表中各表保留的行。
- `verification`：对保留集独立重查全部唯一键、外键与指定冲突，`ok` 为总判定；
  核验失败时进程以退出码 2 结束。

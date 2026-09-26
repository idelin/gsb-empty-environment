# 分层保留策略删除计划

纯 Python 3.11 标准库实现。文件同时受三类约束：**目录继承保留期**、**文件自有保留期**、**法律保留标记**。

## 约束语义

- 可删除时间 = `created_at + max(目录继承下界, 文件自有下界)`，即取全部有效下界的最大值。
- 目录继承下界：沿目录链向上，取**最近一个**设置了 `retention_days` 的目录；均未设置则为 0。
- 法律保留：文件自身或**任一祖先目录**带 `legal_hold` 标记 => 可删除时间为无限（永不删除）。
- 目录移动（`move_dir`）后，整个子树的继承约束立即重算并重建索引。
- 计划基于 `(file_id, version)` 版本快照；执行时版本不一致则跳过并给出理由。
- 最早可删除索引为最小堆 `(deletable_at, epoch, file_id)`，时间推进只弹出堆顶到期项，**不做全量扫描**；移动/标记变更产生的过期堆项通过 epoch 惰性跳过。

## 运行

```bash
python3 retention.py plan SCENARIO.json [--now N]            # 生成删除计划
python3 retention.py execute SCENARIO.json PLAN.json [--now N]  # 执行计划
python3 -m unittest test_retention -v                        # 运行测试
```

## 输入格式（场景 JSON）

```json
{
  "now": 15,
  "directories": [{"id": "R20", "parent": null, "retention_days": 20, "legal_hold": false}],
  "files": [{"id": "f1", "dir": "S", "created_at": 0, "retention_days": 10, "legal_hold": false, "version": 1}],
  "operations": [
    {"op": "move_dir", "dir": "S", "new_parent": "R5"},
    {"op": "move_file", "file": "f1", "dir": "R5"},
    {"op": "set_legal_hold", "file": "f1", "flag": true},
    {"op": "set_dir_legal_hold", "dir": "R20", "flag": true},
    {"op": "modify_file", "file": "f1"}
  ]
}
```

- `retention_days` 可省略（目录省略表示继承上级，文件省略默认为 0）。
- `operations` 按顺序在计划/执行前应用，用于模拟目录移动、法律保留变更、文件修改（版本 +1）。

## 输出格式

`plan` 输出（版本快照）：

```json
{"generated_at": 15, "entries": [{"file_id": "f1", "version": 1, "deletable_at": 10}]}
```

`execute` 输出：

```json
{
  "executed_at": 15,
  "deleted": [{"file_id": "f1", "version": 1}],
  "skipped": [{"file_id": "f2", "planned_version": 1, "current_version": 2,
               "reason": "version changed since plan snapshot"}]
}
```

跳过理由包括：`version changed since plan snapshot`、`legal hold active`、`file no longer exists`、`retention constraint not satisfied at time N`。

## 示例（均为合成数据）

`examples/` 下三个场景对应验收边界：

- `scenario_before_move.json`：父目录下界 20 + 文件下界 10，时间 15 => 计划为空（不可删）。
- `scenario_after_move.json`：目录 S 移动到下界 5 的 R5 后，下界变为 max(5,10)=10，时间 15 => 可删。
- `scenario_version_changed.json`：计划生成后文件被修改，执行时版本不一致 => 跳过并给出理由。

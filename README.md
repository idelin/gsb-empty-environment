# 多快照去重空间回收规划器

删除快照以回收去重存储空间：内容块被多个文件版本共享，快照引用文件版本。
只有当某内容块的**所有**引用方快照都被删除（引用数归零）时，该块才真正可回收。
共享块具有**互补性**——两个快照各引用同一个 10 字节块时，删任一个回收 0，
删两个回收 10，单独可回收量不能简单相加。

本工具在给定每快照业务损失和所需回收字节数时，选出**总损失最小**的待删快照集合；
平局时删除数最少；再平局时按快照 ID 排序后的字典序最小。
求解器为精确分支定界（枚举 + 上界剪枝），天然按引用计数处理共享块互补性，
并支持损失预算界与节点上限。仅使用 Python 3.11 标准库。

## 运行

```bash
python3 dedup_reclaim.py solve input.json [--loss-budget N] [--max-nodes N] [--pretty]
python3 dedup_reclaim.py solve - < input.json     # 从标准输入读取
python3 dedup_reclaim.py example                  # 打印合成示例输入
python3 -m unittest test_dedup_reclaim -v         # 运行测试
```

退出码：找到可行解（`optimal`/`feasible`）为 0；`infeasible`/`budget_exceeded`/`unknown` 为 1；输入非法为 2。

## 输入格式（JSON）

```json
{
  "blocks":    {"b1": 10, "b2": 6},
  "versions":  {"v1": ["b1"], "v2": ["b1", "b2"]},
  "snapshots": [
    {"id": "s1", "loss": 4, "versions": ["v1"]},
    {"id": "s2", "loss": 6, "versions": ["v2"], "blocks": []}
  ],
  "required_bytes": 10
}
```

- `blocks`：块 ID → 字节大小（正整数），必填。
- `versions`：文件版本 ID → 其引用的块 ID 数组，可选。
- `snapshots`：非空数组。每项含唯一 `id`、非负业务损失 `loss`（缺省 0），
  并通过 `versions`（引用文件版本）和/或 `blocks`（直接引用块，便捷写法）关联内容块。
- `required_bytes`：需要回收的字节数（非负整数）。

## 输出格式（JSON）

```json
{
  "status": "optimal",
  "optimal_proven": true,
  "selected_snapshots": ["s1", "s2"],
  "total_loss": 10,
  "reclaimed_bytes": 16,
  "required_bytes": 14,
  "reclaimed_blocks": ["b1", "b2"],
  "nodes_explored": 9,
  "notes": []
}
```

- `status`：`optimal`（已证全局最优）/ `feasible`（达到节点上限，当前最优可行解，未证最优）/
  `infeasible`（删光所有快照也不够）/ `budget_exceeded`（可行解存在但都超预算界）/
  `unknown`（达到节点上限且未找到可行解）。
- `optimal_proven`：搜索是否完整结束、最优性是否得证。未证最优或未验证时会在 `notes` 中明确说明。
- `reclaimed_bytes` / `reclaimed_blocks`：按引用计数对所选集合重算的真实回收量，非单独量相加。

## 算法说明

对快照做二叉分支（保留/删除）的深度优先搜索，逐块维护引用计数：

- `live[b]`：未被删除的引用方数；归零时块计入已回收。
- `kept[b]`：已确定保留的引用方数；用于计算**潜在可回收量**上界
  （所有引用方仍在未决定集合中的块乐观估计都可回收）。
- 剪枝：已回收 + 潜在可回收 < 所需字节 → 剪；损失超过当前最优或预算界 → 剪；
  损失非负，故一旦满足回收约束即记录解，不再加深。

该问题是集合覆盖型 NP-hard 问题，精确搜索复杂度为指数级；`--max-nodes`
可在超大规模实例上换取有界时间内的可行解（此时 `optimal_proven=false`）。

## 示例数据

`examples/` 下均为合成数据：`shared_block.json`（共享块验收场景）、
`fleet.json`（多快照共享基础镜像场景）。

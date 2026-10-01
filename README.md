# 分层配额公平轮转（Deficit Round-Robin）调度器

Python 3.11，仅使用标准库。所有示例数据均为合成数据。

## 组件

| 文件 | 说明 |
| --- | --- |
| `drr.py` | 单层 DRR：租户权重、量子、每任务整数成本、赤字上限、动态权重、暂停/恢复 |
| `tree_drr.py` | 两级（可泛化为多级）租户树 DRR：路径赤字、快照/恢复、叶租户动态迁移 |
| `starvation.py` | 有限状态穷举的饥饿循环检测器，报告最短反例或已检查状态上界 |
| `cli.py` | 可执行命令行（`run` / `check` 两个子命令） |
| `test_drr.py` / `test_tree_drr.py` / `test_starvation.py` | unittest 测试 |
| `examples/` | 合成示例场景 |

## 算法（明确的赤字轮转）

单层（`drr.py`）：

- 每轮每个未暂停且有积压的租户增加 `weight * quantum` 赤字，并以赤字上限截断。
- 只执行队首任务，且仅当其整数成本不超过当前赤字；出队即按成本扣减赤字。
- 队列变空的租户赤字清零（空闲不攒信用）；暂停的租户冻结（不增不减）。
- 成本超过赤字上限的任务在入队时被**明确拒绝**（`reject` 事件 /
  `InfeasibleTaskError`），因为它永远不可能被服务。

树形（`tree_drr.py`）：

- 每个非根节点（父、子）各有权重、量子和赤字上限；根节点不参与赤字约束。
- 叶任务出队要求路径（根以下到叶）上**所有**节点赤字均不小于成本，
  出队时从路径**每个**节点同时扣减。
- 祖先暂停使整个子树冻结（不授信、不服务、赤字保留）。

### 轮次递进规则（避免子层空转耗尽父量子）

1. 每个节点每轮至多授信一次，且仅当其子树存在未暂停的积压时。
2. 空闲节点（子树无积压）在遍历中被遇到时赤字清零——空闲子树不攒信用，
   因此空闲子层无法消耗父节点的服务份额。
3. 暂停节点整条子树跳过，赤字冻结保留。

注意：空闲清零发生在"活跃遍历遇到该节点"时；若整个父子树空闲，
更深层节点的残留赤字会保留到该子树再次活跃并被遍历到时才清零。

### 迁移（禁止凭空增发服务）

`migrate_leaf` 将叶租户挂到新父节点下：叶赤字按**新路径**的最小上限截断
（只减不增，绝不凭空增发服务）；在新路径上限下不可行的任务被丢弃并记录证据。

### 大任务不饿死

任何可行任务（成本 ≤ 路径各节点赤字上限的最小值）都会被执行：路径上每个
有积压的节点每轮赤字增加 `weight * quantum` 直到上限，因此队首任务在有限
轮数内（约 `ceil(cost / (weight * quantum))` 轮量级）必定可服务。
`starvation.py` 的穷举检测器对此给出了有限状态空间下的机器验证。

## 命令行

```sh
python3.11 cli.py run examples/flat_scenario.json    # 单层场景
python3.11 cli.py run examples/tree_scenario.json    # 树形场景
python3.11 cli.py check examples/starve_check.json   # 饥饿穷举检测
```

输出为 JSONL（每行一个 JSON 事件）。事件类型：

- `credit`：授信证据（`weight`/`quantum`/`credit`/`deficit_before`/`deficit_after`/`cap`）。
- `dequeue`：出队证据；树形模式带 `path` 与每个路径节点的
  `deficit_before`/`deficit_after`。
- `head_blocked`：队首成本不足，附 `blocker`（路径上首个赤字不足的节点）
  与 `path_deficits`。
- `reject`：入队拒绝（成本超过赤字上限/路径上限）。
- `reset` / `skip` / `pause` / `resume` / `set_weight` / `migrate` /
  `snapshot` / `enqueue` / `incomplete`。

### 输入格式

见 `cli.py` 模块文档字符串（`python3.11 -c "import cli; help(cli)"`）或
`examples/` 下的三个合成示例。要点：

- 单层：`{"mode":"flat","quantum":Q,"deficit_cap":C,"tenants":[...],"operations":[...]}`。
- 树形：`{"mode":"tree","nodes":[{id,parent,weight,quantum,cap,leaf?,tasks?}],"operations":[...]}`，
  根节点 `root` 自动创建。
- 操作：`run_rounds` / `run_until_idle` / `set_weight` / `pause` / `resume` /
  `enqueue` / `migrate`（树形）/ `snapshot`（树形）。
- 饥饿检测：`{"nodes":[...],"streams":{叶:[成本周期]},"victim_leaf":"A","victim_cost":c,"state_bound":N}`。

## 快照与恢复

`TreeDRR.snapshot()` 返回 JSON 可序列化的完整状态（轮次、各节点赤字、
游标、暂停标志、队列）；`TreeDRR.restore(snap)` 精确重建，后续调度序列
与原调度器逐事件一致（有测试保证）。

## 饥饿检测器

调度器是确定性的，且状态空间有限（赤字受上限约束、游标受扇出约束、
任务流位置受周期约束），从初始状态出发的轨道必为"套索"（前缀 + 循环）。
检测器沿轨道穷举直到状态重复或达到 `state_bound`：

- 循环中没有任何受害者叶出队 → 报告**最短反例**（前缀轮数 + 循环轮数 +
  每轮完整事件证据）。
- 循环每圈都服务受害者 → 证明无饥饿。
- 达到 `state_bound` 仍未重复 → 报告已检查状态上界，结论为未决。

## 测试

```sh
python3.11 -m unittest discover -v
```

实测结果：25 个测试全部通过（含验收边界：权重 1、量子 2、队首成本 5、
上限 5 时第三轮执行；上限 4 时入队即明确拒绝）。

## 诚实性说明

- 公平性为 DRR 标准的按权重比例公平（长期服务量趋近权重比），
  **未证明**任何意义下的最优性；短期边界未做形式化推导。
- 饥饿检测器覆盖的是"确定性调度器 + 周期任务流"的有限状态空间；
  对任意到达序列的全称性无饥饿结论由 DRR 的赤字增长论证给出（上文），
  未做机器验证。
- 快照/恢复的一致性通过确定性重放测试验证，未做跨版本兼容性保证。

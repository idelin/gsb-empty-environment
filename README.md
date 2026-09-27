# snapreclaim — 多快照去重空间回收规划器

内容块（block）被多个文件版本（file version）共享，快照（snapshot）引用文件版本。
删除一组快照后，**只有引用计数归零的块才能回收**。共享块具有互补性：
两个快照各引用同一个 10 字节块时，删任一个回收 0，删两个才回收 10 —
— 因此不能把每个快照"单独可回收量"简单相加。

本工具在给定每快照业务损失（loss）和回收目标时，用**精确分支定界搜索**
选出总损失最小的快照删除集合；平局时依次比较：删除数更少、排序后 ID
列表字典序更小。也支持给定损失预算、最大化回收字节的模式。

仅依赖 Python 3.11 标准库，无第三方依赖。所有示例数据均为合成数据。

## 运行

```bash
python3.11 -m snapreclaim solve INPUT.json --required-bytes N
python3.11 -m snapreclaim solve INPUT.json --loss-budget N
python3.11 -m snapreclaim reclaim INPUT.json --delete snap-1 snap-2
python3.11 -m unittest discover -s tests -v   # 运行测试
```

`INPUT.json` 可用 `-` 表示从标准输入读取。

可选参数（`solve`）：

- `--node-limit K`：最多展开 K 个搜索节点后停止，返回当前最优解，
  输出中 `proven_optimal=false`。
- `--time-limit SEC`：最多搜索 SEC 秒后停止，语义同上。

退出码：`0` 成功（找到可行解或 reclaim 查询完成），`1` 需求不可行，
`2` 输入格式错误。

## 输入格式（JSON）

```json
{
  "blocks": {"blk-1": 10, "blk-2": 20},
  "file_versions": {"fv-1": ["blk-1"], "fv-2": ["blk-1", "blk-2"]},
  "snapshots": [
    {"id": "snap-1", "loss": 4, "file_versions": ["fv-1"]},
    {"id": "snap-2", "loss": 6, "file_versions": ["fv-2"]}
  ]
}
```

- `blocks`：块 ID → 字节大小（正整数），必填。
- `file_versions`：文件版本 ID → 其包含的块 ID 列表，可省略（省略时
  快照的 `file_versions` 无法引用任何版本，一般应提供）。
- `snapshots`：非空列表；每项含唯一字符串 `id`、非负整数 `loss`
  （删除该快照的业务损失）、非空 `file_versions` 列表。快照有效引用
  块集合为其所有文件版本块集合的并集。

## 输出格式（JSON）

`solve` 子命令：

```json
{
  "feasible": true,
  "selected": ["snap-1", "snap-2"],
  "total_loss": 10,
  "reclaimed_bytes": 10,
  "proven_optimal": true,
  "nodes_explored": 7,
  "elapsed_seconds": 0.000123,
  "objective": "min_loss",
  "required_bytes": 10
}
```

- `feasible`：是否存在满足回收目标（或预算约束）的删除集合。
- `selected`：排序后的待删除快照 ID 列表。
- `total_loss` / `reclaimed_bytes`：该集合的总损失与实际回收字节数
  （按共享块引用计数精确计算，可能超过 `required_bytes`）。
- `proven_optimal`：`true` 表示搜索完整结束、结果已证最优；
  因 `--node-limit` / `--time-limit` 提前停止时为 `false`，此时
  返回的是当前最优解（未证最优）。

`reclaim` 子命令输出 `{"deleted": [...], "reclaimed_bytes": N}`，用于
直接核算给定删除集合的回收量。

## 目标与平局规则

- `--required-bytes N`：在回收量 ≥ N 的集合中，最小化 `total_loss`；
  平局依次取删除数更少、排序后 ID 列表字典序更小者。
- `--loss-budget N`：在 `total_loss` ≤ N 的集合中，最大化回收量；
  平局依次取损失更小、删除数更少、ID 字典序更小者。

## 算法与正确性

精确分支定界。按固定顺序逐个决定快照"删/留"，搜索中维护每个块的：

- `live`：未决定 + 保留（不删）快照对它的引用数；归零即被回收；
- `undecided`：未决定快照对它的引用数；
- `potential`：仍可能被释放的块（`live == undecided > 0`）的字节和，
  作为剩余可回收量的乐观上界用于剪枝。

因为回收量直接由引用计数归零事件累计，共享块的互补性被完整建模，
不做任何"单独可回收量相加"的近似。未触发节点/时间限制而结束时，
结果即为已证最优（测试中与穷举枚举在随机合成实例上交叉验证）。

复杂度：最坏 O(2^n · B)（n 为快照数，B 为块数），精确搜索本质上是
指数级的；大规模实例请使用 `--node-limit` / `--time-limit` 获得
带最优性缺口说明的近似结果。

## 目录结构

- `snapreclaim/model.py` — 数据模型、输入校验、回收量核算
- `snapreclaim/solver.py` — 精确分支定界求解器（两种目标）
- `snapreclaim/cli.py` — 命令行入口
- `examples/` — 合成示例数据
- `tests/` — unittest 测试（含验收边界与穷举交叉验证）

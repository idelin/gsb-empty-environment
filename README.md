# 分层配额公平轮转（Deficit Round Robin）调度器

Python 3.11 标准库实现，无第三方依赖。本仓库所有示例数据均为合成数据。

## 组件

| 文件 | 说明 |
|---|---|
| `drr_core.py` | 单级赤字轮转（DRR）调度器 |
| `hdr_core.py` | 两级/任意深度租户树 DRR，含快照、恢复、叶迁移 |
| `starvation.py` | 有限状态穷举饥饿检测器 |
| `cli.py` | 命令行入口 |
| `test_drr.py` | unittest 测试（17 例） |
| `examples/*.json` | 合成示例配置 |

## 单级 DRR 规则（`drr_core.py`）

- 一轮 = 按固定顺序遍历所有租户一次。
- 租户**活跃**（未暂停且队列非空）才获得配额注入：`deficit = min(deficit + weight * quantum, cap)`。
- 队首任务成本 `<= deficit` 才出队，出队即扣减；严格 FIFO，小队首后任务不得插队。
- 赤字上限 `cap`：队首成本 `> cap` 的任务**永远不可执行**，到达队首时被**明确拒绝**（移入 `rejected` 并输出证据），不阻塞后续任务。
- 支持事件：`pause` / `resume` / `set_weight` / `enqueue`（在指定轮开始前生效）。
- 反饥饿论证：成本 `<= cap` 的队首任务，每个活跃轮赤字至少增加 `weight*quantum >= 1` 直至封顶，故必被执行（由 `starvation.py` 对具体配置穷举验证）。

## 层级 DRR 轮次递进规则（`hdr_core.py`）

- **R1** 一轮 = 对树做一次固定顺序 DFS。
- **R2** 每个节点每轮**至多注入一次**配额，且仅当其活跃（未有效暂停且子树有待办任务）。父节点无论被子层访问多少次，每轮只注入一次 → 子层空转不会耗尽父量子；空闲子树不注入。
- **R3** 有效暂停：节点自身或任一祖先暂停 → 整棵子树跳过（不注入、不服务）。
- **R4** 叶任务出队条件：成本 `<=` 根到叶路径上**每个**节点的赤字；出队时沿路径**同时原子扣减**，并输出路径赤字证据（`path_deficits_after`）。
- **R5** 队首成本超过路径最小上限 → 永久不可行，明确拒绝并记录。
- **迁移**：叶租户可迁移到新父节点；已有赤字按**新路径**各节点上限截断（只减不增），禁止凭空增发服务。
- **快照**：`to_snapshot()` / `from_snapshot()` 序列化完整状态（结构、赤字、队列、暂停位、轮号），JSON 可持久化。

## 饥饿检测（`starvation.py`）

调度器完全确定，可达状态图是"路径 + 环"的函数图。状态定义为
`(全部节点赤字, 各流叶位置 mod 流长)`，空间有界：
`∏(cap_i + 1) × ∏(len(stream_j))`。

- 枚举状态直到重复或达到 `--max-steps`。
- 环内若某**可服务**叶（流中存在成本 `<=` 路径最小上限的任务）从未被服务 → 永久饥饿，输出最短反例（确定性系统首次重复状态即最短）及环轨迹。
- 未找到重复状态 → 报告 `INCONCLUSIVE` 及已检查状态数与状态空间上界。

## 命令行

```bash
# 单级模拟（JSONL 输出每轮证据）
python3.11 cli.py run  --config examples/single_acceptance.json
python3.11 cli.py run  --config examples/single_dynamic.json      # 暂停/恢复/动态权重

# 层级模拟 + 快照保存/恢复
python3.11 cli.py hrun --config examples/hier.json --snapshot-out snap.json
python3.11 cli.py hrun --config examples/hier.json --snapshot-in snap.json --rounds 2

# 饥饿检测（退出码：0 无饥饿，1 发现反例）
python3.11 cli.py check --config examples/check_ok.json
python3.11 cli.py check --config examples/check_starve.json --out counterexample.json

# 测试
python3.11 -m unittest test_drr -v
```

## 输入格式（JSON 配置）

单级（`run`）：
```json
{
  "rounds": 5,
  "tenants": [{"id": "A", "weight": 1, "quantum": 2, "cap": 5,
               "tasks": [{"tag": "A#big", "cost": 5}, 1]}],
  "events": [{"round": 2, "op": "pause", "tenant": "A"},
             {"round": 4, "op": "set_weight", "tenant": "A", "weight": 3}]
}
```

层级（`hrun` / `check`）：
```json
{
  "rounds": 8,
  "tree": {"id": "G", "weight": 1, "quantum": 6, "cap": 12,
           "children": [
             {"id": "t1", "weight": 1, "quantum": 2, "cap": 5, "tasks": [5, 1]},
             {"id": "sub", "weight": 2, "quantum": 3, "cap": 8,
              "children": [{"id": "t2", "weight": 1, "quantum": 2, "cap": 4,
                            "stream": [3, 1]}]}]},
  "events": [{"round": 3, "op": "pause", "node": "sub"},
             {"round": 6, "op": "migrate", "leaf": "t3", "new_parent": "G"}]
}
```
- `tasks`：有限任务流（整数成本或 `{"tag","cost"}`）；`stream`：循环无限任务流（供饥饿检测）。
- 事件 `op`：`pause` / `resume` / `set_weight` / `enqueue` / `migrate`（仅层级）。

## 输出格式

`run` / `hrun` 输出 JSONL，每行一个轮次记录或事件记录：

- 单级轮次：`{"round", "visits": [{"tenant", "deficit_before_credit", "credit",
  "deficit_after_credit", "cap", "served": [{"task","cost","deficit_after_dequeue"} |
  {"task","cost","rejected","reason"}], "head_blocked"?, "deficit"}]}`。
- 层级轮次：`{"round", "credits": [{"node","deficit_before","credit","deficit_after","cap"}],
  "dequeues": [{"leaf","task","cost","served","path_deficits_after"} |
  {"leaf","blocked","blocker","path_deficits","reason"}],
  "rejections": [...], "skipped": [...], "deficits": {node: deficit}}`。
- `check` 输出单个 JSON：`{"result": "NO_STARVATION"|"STARVATION"|"INCONCLUSIVE",
  "states_explored", "state_space_bound", "cycle_start_round", "cycle_length",
  "starved_leaves", "counterexample"?}`。

## 验收边界（已由测试与 CLI 实证）

- 权重 1、量子 2、队首成本 5、上限 5：赤字序列 2 → 4 → 5，**第 3 轮出队**。
- 上限 4 < 成本 5：第 1 轮即**明确拒绝**（`"cost 5 > deficit cap 4: can never be served"`）。

## 已知限制

- 饥饿检测针对无事件的静态配置；含动态事件的场景不做穷举（状态含事件脚本后不再是函数图）。
- `INCONCLUSIVE` 仅表示在步数上界内未发现环，不构成饥饿不存在的证明。
- 调度顺序为固定 DFS 序（非旋转指针），公平性由赤字积累保证。

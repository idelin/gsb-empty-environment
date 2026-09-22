# wmcoord — 水位线协调内核

纯 Python 3.11 标准库实现的水位线（watermark）协调内核：在本地离散事件
模拟器中控制节点、消息、时钟与故障，决定下游何时可以安全封闭窗口。
无任何第三方依赖，不访问网络。

## 组成

| 模块 | 职责 |
| --- | --- |
| `wmcoord/model.py` | 数据/进度消息与全部命令类型、提交结果、协议错误 |
| `wmcoord/engine.py` | 纯状态机：缺口与确认索引、有效水位、空闲/参与、epoch、交接 |
| `wmcoord/journal.py` | CRC 帧日志（FileLog/MemoryLog）、截断尾恢复、中间损坏拒绝、校验和检查点 |
| `wmcoord/kernel.py` | 持久化内核：WAL、稳定决策 ID、发布/确认、崩溃钩子、重放与修复 |
| `wmcoord/simulator.py` | 离散事件模拟器：协调器/下游节点、虚拟时钟、崩溃与重启注入 |
| `tests/reference_checker.py` | 独立参考校验器（不 import 生产代码，暴力扫描实现同一规则） |
| `tests/` | 场景、持久化、故障等价、有限状态探索测试 |
| `samples/replay_demo.py` | 重放样例：场景 + 检查点 + 崩溃恢复 + 交接 + 发布 |
| `samples/benchmark.py` | 成本量测：消息数/来源数增长、恢复单趟扫描 |

## 核心规则（实现于 `engine.py`）

- 数据消息带 `source/epoch/seq/ts`，`seq` 从 1 连续；进度消息带
  `(frontier, cut_seq)`，承诺 `cut_seq` 之后不再产生 `ts < frontier`
  的数据（frontier 为排他边界）。
- 来源有效水位只能在 `1..cut` 全部到达**且**被处理器显式确认后推进到
  `frontier`；进度可先于数据到达，重复与乱序不能绕过缺口。
- 全局边界 = 参与来源有效水位的最小值，永久单调。空闲超时让无待处理
  数据的来源暂时退出最小值计算；仍有未确认数据或 cut 内缺口的来源即使
  超时也继续阻挡。全部空闲时保留上次边界，不跳到无穷。
- 低于已封闭边界的数据进入迟到通道（`state["late"]`），不改已封闭结果。
- 新来源必须以当前全局边界为接入下限并显式接受（`JoinSource`）。
- epoch 不可复用；旧 epoch 数据进迟到通道，旧 epoch 进度被忽略。
- 父分区交接：`BeginHandoff(parent, H, children)` 要求 `H >= 当前全局边界`；
  父 cut 内全部确认、父有效水位达到 H、且所有子分区确认从 H 接管后
  **原子**完成（同一日志事务写 `HANDOFF_DONE`）。之前父仍参与，之后父
  退出、各子从 H 参与。交接可幂等重试；未完成可取消；完成后不可取消。

## 持久化与恢复（`journal.py` / `kernel.py`）

- 帧格式：`[4B 长度][JSON payload][4B CRC32]`，文件头 `WMLGv1\n`。
- 每个命令是一个事务：`CMD` 记录后跟其效果记录（`DECISION` /
  `HANDOFF_DONE`）；发布与确认是独立命令事务。
- 封闭决策先落盘再发布，ID 稳定（`D00000001…`，由重放确定性地重现）。
  发布后确认前崩溃，恢复时用**同一 ID** 重发，下游按 ID 去重。
- 截断尾（torn tail）：尾部坏帧直接截断；中间损坏（坏帧之后还有好帧）
  拒绝打开，抛 `CorruptLogError`。
- 检查点保存缺口/确认索引、交接状态、未确认决策与日志位置，CRC 校验 +
  原子替换；损坏的检查点回退为全量日志重放。
- 恢复时若事务缺效果记录（命令与效果之间撕裂），按确定性规则补齐，
  不会生成第二个决策 ID。
- 崩溃注入点：`before/after_command_write`、`after_decision_write`、
  `before/after_publish_write`、`before/after_handoff_write`。

## 缺口与确认索引的回收

每个 (source, epoch) 维护一对**连续前缀计数器**（`arrived_prefix` /
`acked_prefix`）加三个只存乱序尾的集合（`arrived` / `acked` /
`early_acks`）以及 `seq -> ts` 映射：

- 数据/确认到达时 O(1) 更新；前缀指针按需滑过集合元素，每个 seq 只进出
  集合一次，整个 epoch 生命周期总工作量与消息数成线性。
- 当 `1..cut` 全部到达并确认（`_epoch_complete`），`_reclaim_index`
  删除所有 `seq <= cut` 的事件时间映射；前缀计数器已覆盖该区间，集合中
  只剩 `> cut` 的尾部（可能被未来更大的 cut 使用）。
- 来源有效水位达到 frontier 后，该 cut 之前的全部索引空间即被回收。

## 更新成本

- 每条数据/确认消息：索引更新摊还 **O(1)**；随后重算全局最小值为
  **O(S)**（S = 来源数），因为参与集合随 idle/exit/join 动态变化。
  如需更大规模可换惰性删除堆降到 O(log S)，当前实现选择简单可验证。
- 交接完成：O（子分区数）。
- **任何消息处理都不读日志**；日志只在 `Kernel.open` 时单趟扫描
  （见 `samples/benchmark.py`：4 万命令恢复约 0.3s）。

## 运行

```bash
python3 run_tests.py            # 全部测试，结果写入 tests/results/latest.txt
python3 samples/replay_demo.py  # 重放样例（含崩溃恢复与交接）
python3 samples/benchmark.py    # 成本量测
```

## 测试

- `tests/test_scenarios.py` — 题目规定的规范场景（缺口阻挡、未确认阻挡、
  空闲推进、交接两子确认、旧父消息失效等）。
- `tests/test_persistence.py` — 截断尾、中间损坏拒绝、检查点回退、
  决策 ID 跨重启稳定、发布/确认恢复。
- `tests/test_faults.py` — 故障注入等价：相同逻辑输入与超时历史下，
  恢复后的下游封闭序列（经 ID 去重）与无故障运行完全一致。
- `tests/test_exploration.py` — 有限状态探索：枚举小消息集的全部合法
  投递/确认顺序（5040/720/120/24 种排列），逐步比对生产内核与独立参考
  校验器（边界、决策序列、迟到分类、错误行为），并在每条排列末尾执行
  一次崩溃+重开恢复再比对；参考校验器同时直接断言安全不变式（未确认
  数据不被封闭、边界不回退、分区不遗漏）。

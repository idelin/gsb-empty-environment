# 水位线协调内核

这是一个只依赖 Python 3.11 标准库的本地离散事件实现。节点、消息投递、显式处理确认、逻辑时钟、空闲超时、分区交接和进程崩溃都由库内模拟器或测试夹具控制。

## 语义

- 数据消息包含 `source`、`epoch`、从 1 开始的连续 `seq` 和整数事件时间。
- 进度消息的 `frontier` 是排他边界；`(cut_seq, frontier)` 表示该来源在 `cut_seq` 之后不再生成事件时间小于 `frontier` 的数据。
- 来源有效水位只在 `1..cut_seq` 连续到齐并且这些数据都被处理器显式确认后，才允许取该承诺的 `frontier`。
- 进度先于数据、乱序数据、重复数据和重复确认都不能绕过缺口；未知确认不会推进状态。
- 全局边界是当前参与来源有效水位的最小值，并通过持久决策保持永久单调。
- 空闲超时可以把来源暂时移出最小值计算，但不会清除未确认数据或已声明 cut 的缺口；这些来源仍阻挡推进。
- 所有来源都空闲时保留上次全局边界，不会跳到无穷。
- 事件时间小于已封闭边界的新数据进入迟到通道并持久化，但不修改已封闭结果。
- 新来源先取得当前全局下限，再用相同下限显式接受；接受前不参与最小水位。

## 分区交接

`begin_handover(handover_id, parent, parent_epoch, H, terminal_cut, children)` 原子注册父分区、终止 cut 和所有子分区。

- `H` 必须不小于当前全局边界。
- 交接完成前父仍参与最小值；完成后父退出，所有子从 `H` 参与。
- 完成条件是：父 `terminal_cut` 内数据连续到齐且全部确认、父有效水位达到 `H`、每个子分区都用登记的 epoch 显式确认接管。
- 子的首个 frontier 不得低于 `H`；子事件时间低于 `H` 会被拒绝。
- 重试登记同一个 handover 或重复参与者会被拒绝；取消只允许发生在完成前。
- 完成后取消会失败，父 epoch 被归档，旧父消息只能得到 stale-epoch 拒绝，不能再推动边界。

## 持久化和崩溃恢复

每条状态迁移先追加到 `watermark.log`，帧格式包含魔数、Base64 JSON、CRC32 和换行符：

- 决策先写日志，再调用发布回调。
- 决策 ID 为稳定的 `decision-000000000001` 形式，序号只从已恢复的决策计数增长。
- 已发布但未确认的决策在恢复时按相同 ID 重发；下游用稳定 ID 去重。
- checkpoint 原子替换保存缺口、处理确认、交接状态、迟到数据、决策集合和日志位置。
- 末尾无换行或校验失败的一条记录按截断尾处理并截掉；其后仍有记录的损坏帧会导致拒绝打开。
- checkpoint 校验失败会显式抛错，避免用损坏快照继续。

故障注入点包括：

- `decision_pre_append`
- `decision_post_append_pre_publish`
- `decision_post_publish_pre_ack`
- `handover_pre_commit_record`
- `handover_post_commit_record`
- `handover_post_commit_state`

## 库用法

```python
from watermark_kernel import DataMessage, ProgressMessage, WatermarkKernel

k = WatermarkKernel.open("/tmp/wm-state", checkpoint_every=16, publisher=publish)
k.proposal("A", "epoch-a")
k.accept_source("A", "epoch-a", k.global_boundary)
k.progress(ProgressMessage("A", "epoch-a", 100, 3))
k.data(DataMessage("A", "epoch-a", 1, 1))
k.ack("A", "epoch-a", 1)
k.ack_decision("decision-000000000001")
```

直接运行本地重放样例：

```bash
python examples/replay_demo.py
```

## 索引回收和更新成本

每个来源维护：

- `rx_prefix`：连续到齐的最大 seq。
- `ack_prefix`：已处理确认的连续最大 seq。
- `pending_received`：高于 `rx_prefix` 的乱序到齐数据。
- `unacked`：已到齐但尚未确认的事件时间。
- `claims_heap`：尚未被确认前缀释放的 `(cut_seq, frontier)` 承诺。

当缺口补齐时，连续段立即从 `pending_received` 移入前缀并删除；确认连续段时，`unacked` 中对应项被删除，满足条件的 cut 承诺从最小堆弹出。因此已封闭前缀不再保存逐消息索引，内存只保留缺口、乱序尾部、未确认尾部和未满足承诺。

- 数据到达：均摊 `O(1)`，最坏会一次合并一段连续缓存。
- 处理确认：均摊 `O(log C)`，`C` 是该来源未满足承诺数；弹出项不会重复扫描。
- 进度承诺：`O(log C)`。
- 一次输入后的全局封闭决策：当前实现扫描活跃来源，`O(S + log C)`；决策发布和持久化是一次顺序追加，不扫描历史日志。
- 交接完成：`O(S + K)`，`K` 是子分区数量。
- checkpoint：序列化当前仍活跃的状态；恢复只从 checkpoint 位置顺序读取后续日志。

## 测试

```bash
python -m unittest discover -s tests -v
```

`tests/independent_oracle.py` 中的 `IndependentWatermarkOracle` 独立计算可封闭边界，不调用生产协调器生成预期值。测试枚举 280 个合法数据/确认/进度全序以及子确认顺序，并独立检查：

- 未到齐或未确认的数据不会被封闭。
- 全局边界不回退。
- 父分区不会遗漏任何一个子确认。

实际运行结果见 `TEST_RESULTS.md`。

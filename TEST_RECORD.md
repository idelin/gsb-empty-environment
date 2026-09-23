# 测试记录

环境：macOS (darwin)，Python 3.13.12（代码仅使用 3.11 标准库语法与模块），pytest 8.4.1。
复现：`python3 -m pytest -q`（工作区根目录），`python3 examples/replay_demo.py`。

最近一次运行：**41 passed in 0.23s**。

## 场景覆盖与结果

| 测试文件 | 场景 | 结果 |
|---|---|---|
| tests/test_build_side.py | 屏障先到后补数据、重发幂等、乱序应用、未知 epoch 丢弃、重建隔离与旧 epoch 释放 | 5 passed |
| tests/test_filters.py | 注入常数哈希制造 Bloom 全碰撞；误判只增加表探测（3 vs 2）不改结果；精确过滤器无误判 | 3 passed |
| tests/test_engine.py | 内/左外连接、重复键笛卡尔重数、null 拒绝/安全两种模式 × 3 策略、完整过滤器排除→null 补齐行、WAIT 反压、BYPASS 提前输出+残余补探、epoch 隔离与释放 | 13 passed |
| tests/test_spill.py | 内存→磁盘→双额度满反压、失败整批回滚、反压不丢行、spill 行等待自己 epoch、临时文件 drain 后删除 | 4 passed |
| tests/test_recovery.py | 每个事件位置注入故障（checkpoint 前置）× 3 策略、未确认输出同 ID 重发并由消费端去重、checkpoint 含输入位置/分区完整性/待处理批次/未确认输出、版本切换后崩溃重放 | 7 passed |
| tests/test_interleavings.py | 屏障/最后构建消息/版本切换/扫描批次 5 事件全排列（120）× 3 策略 × 2 连接类型；种子化 fuzz（40 轮：多分区、双 epoch、崩溃、Bloom 小位图/常数哈希）对照嵌套循环 | 7 passed |
| tests/test_checkpoint_atomicity.py | 回归：checkpoint 后 drain 删除 spill 文件再崩溃，恢复不得丢行（checkpoint 对临时文件原子） | 1 passed |
| tests/test_replay.py | 重放已提交日志 examples/replay_log.jsonl，输出逐条一致，崩溃导致 ≥1 次同 ID 重发被去重 | 1 passed |

## 加重验证（非提交用例，开发期执行）

- 300 轮通用 fuzz（策略/连接/null 模式/过滤器/哈希全随机，含崩溃）：全部与嵌套循环多重集合一致，旧 epoch 与临时文件全部释放。
- 300 轮紧内存额度（0–2 行）+ 崩溃的磁盘 spill fuzz：全部一致，无文件泄漏。

## 开发中发现并修复的问题

1. spill 磁盘行经 pickle 往返后与 `batch.rows` 中的 `RowState` 失去对象同一性，
   导致恢复出的行被"解决"而原行永远 pending → 改为 spill 只存 `(batch_id, 行下标)`。
2. checkpoint 与临时文件非原子：checkpoint 后 drain 删除 spill 文件，崩溃恢复时
   磁盘行丢失 → spill 文件字节随 checkpoint 一起走，恢复时原子写回并清理孤儿文件。
3. fuzz 生成器最初把构建消息随机指派分区，与扫描端哈希分区不一致（注入常数哈希时
   暴露）→ 生成器改用与引擎相同的分区函数。

## 重放例子

`python3 examples/replay_demo.py` 输出：

```
recorded 7 output deliveries to examples/replay_log.jsonl
replayed OK: 7 deliveries identical, 3 redeliveries deduped, 6 distinct output rows
  s0#0: 1 rows
  s0#1: 1 rows
  s1#0: 2 rows
  s0#0: 1 rows   <- 崩溃后同 ID 重发，消费端去重
  s0#1: 1 rows   <- 同上
  s1#0: 2 rows   <- 同上
  s2#0: 2 rows
```

脚本覆盖：屏障先到（last_seq=2 时 seq 2 未到）、数据重发、BYPASS 提前输出 +
残余补探、版本切换（epoch 1→2）、崩溃后重放、左外连接 null 补齐。

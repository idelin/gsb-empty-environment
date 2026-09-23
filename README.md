# dynjoin — 可暂停/恢复的分区哈希连接模拟器

纯 Python 3.11 标准库实现。模拟的场景：动态过滤器（精确集合或 Bloom）尚未收齐数据时，
扫描端不能把 `不在过滤器里` 当成 `不匹配`，否则会漏行。
消息、输入批次、时钟与故障全部由本地测试调度，不接任何消息服务。

## 设计要点

**构建端**（每个分区 = epoch + 单调 seq + 结束屏障）
- `BuildPartition` 维护 `applied seq 集合 + barrier(last_seq)`；
  只有 `barrier 到达 且 所有 seq <= last_seq 都已应用` 时过滤器才完整。
- 屏障先到、数据重发（按 seq 幂等去重）、乱序都被正确处理。
- 重建产生新 epoch；旧 epoch 按对象身份绑定，正在运行的旧批次继续用旧版本，
  无使用者后释放（`refs == 0` 且非当前版本）。

**扫描端三种策略**（`Strategy`，引擎参数）
- `WAIT`：批次进入有界内存等待队列，满了反压。
- `SPILL`：行落入 `SpillBuffer`（内存额度 + 临时磁盘额度，两者都满则
  整批拒绝并回滚暂存——反压但不丢行），分区完整后按分区 drain。
- `BYPASS`：立即对**部分**构建表执行连接（不碰过滤器），已发射的匹配
  按 build seq 记录为残余，分区完整时再补探，只发增量；左外连接的
  null 补齐行只在完整后产出。

**连接语义**
- 内连接 / 左外连接；`NullMode.REJECT`（null 永不匹配）或
  `NullMode.SAFE`（null = null）；重复键保留完整笛卡尔重数。
- 左外连接中，被**完整**过滤器排除的扫描行输出 null 补齐行，而不是删除；
  Bloom 误判（可注入哈希制造碰撞）只会增加表探测次数，不改变结果。

**输出与恢复**
- 输出批次 ID 稳定（`<scan_batch>#<n>`，由确定性状态机产生）。
- `checkpoint()` 保存：输入位置 `scan_position`、分区完整性
  （`applied`/`barrier`）、待处理扫描批次（等待队列、spill 内存行、
  spill 磁盘文件字节、BYPASS 残余）、未确认输出 `unacked`。
  spill 磁盘文件的字节随 checkpoint 一起走，保证 checkpoint 对临时文件
  的原子性（见 `tests/test_checkpoint_atomicity.py` 的回归场景）。
- 恢复后按脚本前缀重放（生产者至少一次重投；引擎按 seq/批次 id 幂等），
  未确认输出会以同 ID 重发，由测试消费端 `TestConsumer` 按 ID 去重。

**正确性对照**
- `reference.nested_loop_join`：不用过滤器的独立嵌套循环连接。
- 测试枚举"屏障 / 最后一条构建消息 / 版本切换 / 扫描批次"的全部交错
  （5 事件全排列 × 3 策略 × 2 连接类型），并在每个事件前后注入故障；
  另有种子化 fuzz（多分区、多 epoch、崩溃、Bloom 碰撞）。
  所有运行检查去重后的结果多重集合与对照一致，且临时文件与旧 epoch
  在无使用者后释放。

## 目录

- `dynjoin/` — 模拟器实现（messages / filters / build_side / spill / engine /
  consumer / harness / reference）
- `tests/` — pytest 测试（语义、交错枚举、故障注入、spill 配额、重放）
- `examples/replay_demo.py` — 录制 + 重放例子；`examples/replay_log.jsonl`
  为已提交的重放日志
- `TEST_RECORD.md` — 测试记录

## 运行

```bash
python3 -m pytest -q            # 全部测试
python3 examples/replay_demo.py # 录制并重放示例脚本
```

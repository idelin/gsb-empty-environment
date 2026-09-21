# 可暂停/恢复的去抖器（纯事件时间）

仅依赖 Python 标准库。时间完全由测试驱动：观测值经 `ingest(source, value, ts)` 摄入，
时钟经 `advance(ts)` 推进，代码中没有任何真实 sleep。

## 语义

- 候选值必须**持续稳定满 `threshold`** 才确认：`now - since >= threshold`（边界取等号）。
  门限 3 时 `A@0, B@1, B@4`：B 自 t=1 起为候选，t=4 满足 `4-1>=3`，确认 B。
- **未知值**（`value=None`）立即清空候选及其稳定锚点；之后 B 要重新计时。
  `A@0, B@1, unknown@3, B@4` 在 t=4 不确认，需稳定到 t=7。
- **同时刻多事件**按摄入顺序生效：同一 `ts` 上以最后一个值为候选；若该时刻含未知，
  批处理结束后无候选；同一时刻“先未知后值”则以该 `ts` 为锚点。
- **重复事件不延长计时**：候选相同则保留原始 `since`，不重置、不续期；已确认的值
  不会因重复观测再次下发（确认是相对“上次确认值”的边沿）。
- **来源隔离**：每个 source 独立保存 `(candidate, since, last_confirmed)`，互不影响；
  多个来源同一时刻成熟时，按来源创建顺序下发。
- 时钟单调，事件或 `advance` 倒退抛 `ValueError`。

## 操作

- `ingest(source, value, ts)` / `ingest_unknown` / `ingest_batch`：摄入（摄入即折叠）。
- `advance(ts)`：纯推进时间，成熟的候选在此确认。
- `drain()`：取出并删除全部已确认项（确认顺序）；`pending` 只查看不消费。
- `save()` / `to_json()` / `restore()` / `from_json()`：快照与恢复。

## 持久化了什么、何时释放旧事件

事件在**摄入当下**就被折叠进每源三元组 `(candidate, since, last_confirmed)`，
随后立即释放——不保留任何事件历史。快照只含：

- 全局：`threshold`、当前 `now`、outbox 序号计数器；
- 每源：`candidate`、锚点 `since`、`last_confirmed`（区分“从未确认”）；
- **未消费 outbox**：已生成但尚未 `drain` 的确认项。

由此保证恢复语义：

1. **尚未满足时长的候选不丢**：`(candidate, since)` 持久化，恢复后剩余时间继续计算。
2. **已确认未消费**：留在 outbox，恢复后由 `drain()` 恰好再投递一次。
3. **已消费不重发**：`drain` 后该项离开持久化状态；配合 `last_confirmed`，
   即使再 save/restore 或收到重复观测也不会再次下发同一变化。

## 测试

```
python3 -m unittest test_debouncer -v
```

`test_debouncer.py` 为可复现事件回放：同一脚本分别“不间断”和在检查点“暂停/恢复”回放，
输出必须一致。三个检查点逐一验证：候选刚开始、推进到即将确认（`advance(3)`）、
确认已生成但尚未 `drain`。

# A→B 无 C 流式事件模式

使用 Python 标准库实现。模式针对同一个 `key`：

1. 先出现 A。
2. B 在 A 的事件时间之后，且 `B.ts <= A.ts + 5ms`（右端点包含）。
3. A 与 B 的事件时间全序之间没有 C。
4. 一个 B 可以同时匹配所有仍有效的 A；一个 A 也可以匹配窗口内的多个 B，因此允许重叠匹配。

## 事件顺序与端点

事件先按 `(timestamp, tie, source, seq)` 排序。

- 同刻默认事件类型顺序为 `A < C < B`。
- 显式提供 `tie` 时，以 `tie` 优先，可表达同刻事件的外部已知顺序。
- “A 后”使用严格全序：`A.order < B.order`。
- “期间”使用开区间：`A.order < C.order < B.order`；C 与 A 或 B 在全序上同点不算“期间”。
- 5ms 窗口按事件时间右端点封闭：`B.ts == A.ts + 5` 匹配，`B.ts == A.ts + 6` 不匹配。

因此默认同刻 `A、C、B` 中 C 位于 A 与 B 之间；若显式顺序是 `C、A、B`，则 C 不阻断该匹配。

## 水位线与迟到

`PatternMatcher(gap_ms=5, allowed_lateness_ms=L)`：

- B 到达时先生成 `PROVISIONAL_MATCH`，消费者可撤回。
- 水位线满足 `A.ts + 5 <= watermark` 时关闭该 A 的窗口：
  - 仍存在的匹配输出 `FINAL_MATCH`；
  - 没有匹配输出 `NO_MATCH`。
- 迟到但仍可修订的记录满足 `event.ts >= watermark - L`：
  - 迟到 C 撤销受影响的已最终匹配，输出 `LATE_NO_MATCH`；
  - 迟到 B 可补出 `LATE_MATCH`；
  - 撤回 A 可撤销未最终或仍在更正期内的匹配。
- 小于 `watermark - L` 的记录不修改状态，输出 `OUT_OF_SCOPE_LATE` 作为侧报告。
- 水位线超过事件的更正截止时间后清理对应状态；身份去重索引也按相同更正期限有界释放。

事件身份固定为 `(source, seq)`。相同身份且载荷相同的重复事件只生效一次；相同身份但 `kind/key/timestamp` 等载荷冲突会抛出 `ValueError`。

## 运行

```bash
python3 consumer_example.py
python3 -m unittest discover -s tests -v
```

`reference.py` 对小流按事件时间做全排序枚举。测试枚举所有事件到达顺序，并比较一次性、不同分块和滚动水位线下的最终匹配集合。

## 示例中的 C 语义

- `A@0、A@1、B@3` 生成两个可撤销匹配；补 `C@2` 后，两个匹配都撤销。
- `B@6` 只匹配 `A@1` 指没有 C 的独立基线，因为 `A@0` 已超出 5ms。
- 如果同一个流中保留 `C@2`，则 `A@1.order < C@2.order < B@6.order`，所以 `A@1→B@6` 也不会匹配。这正是“之间没有 C”的事件时间含义。

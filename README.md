# trigsync

多条有理周期/相位触发流在时间窗内的**共同触发集合**，使用精确同余（CRT）
而非浮点逐步枚举。纯 Python 3.11 标准库，无第三方依赖。

## 核心概念

- **精确时间**：所有数值为 `fractions.Fraction`。`TickScale` 取全体有理量分母
  的 lcm 作为统一刻度（1/denominator = 1 tick），换算为任意精度整数 tick，
  **不溢出、不丢精度**。
- **三种时间类型严格区分**：`Instant`（时间点）、`Duration`（时长）、
  `WallClock`（墙钟）。`Instant + Instant` 等非法运算是类型错误。
  `OffsetTable` 是合成偏移转换表，提供分段线性的 `Instant <-> WallClock`
  精确映射。
- **流规则（均带版本号）**：`PhaseRule`（相位切换，后规则覆盖先规则）、
  `PauseRule`（半开暂停区间，抑制周期触发）、`ExceptionRule`（例外触发点，
  暂停中仍触发）。
- **编译为分段约束**：以相位切换点为边界切段，每段内各流同余用 CRT 合并；
  暂停区间取并集后按区间扣除（非逐 tick）。
- **惰性产出**：`iter_points()` 生成器按区段产出；`count()` 用整式计数；
  `kth(k)` 用计数二分定位——10^15 量级的窗口也无需逐 tick 扫描。
- **例外点必须共同**：某流的例外点只有在其余每条流于该时刻也触发
  （同余命中或自身例外）时才并入共同集合；非共同例外被拒绝。
- **无解证据**：CRT 冲突时 `reports` 给出算术证据（冲突同余、gcd、余数差）。
- **Checkpoint**：`engine.checkpoint.digest()` 绑定所有流的全部规则版本，
  任一规则或其版本号变化都会改变摘要。

## 快速开始

```python
from trigsync import Intersection, PauseRule, PhaseRule, Stream

a = Stream("A", (PhaseRule(1, 0, 2, 0),))          # 周期 2 相位 0
b = Stream("B", (PhaseRule(1, 0, 3, 1),))          # 周期 3 相位 1
eng = Intersection([a, b], 0, 30)
print(eng.first().value)                            # 4

a2 = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(1, 4, 5),))
eng2 = Intersection([a2, b], 0, 30)
print(eng2.first().value)                           # 10（[4,5) 被排除）
```

## 运行

```bash
python3.11 examples/demo.py                 # 可运行样例
python3.11 -m unittest discover -s tests -v # 测试（23 个）
```

## 目录

- `trigsync/timebase.py` — Instant / Duration / WallClock、OffsetTable、TickScale
- `trigsync/crt.py` — 精确 CRT（模数不必互素），无解时给出结构化证据
- `trigsync/streams.py` — 版本化的相位/暂停/例外规则与流摘要
- `trigsync/engine.py` — 分段编译、惰性产出、count / kth、Checkpoint
- `examples/demo.py` — 七个场景的可运行样例
- `tests/test_trigsync.py` — 含小范围穷举对照与巨大窗口测试

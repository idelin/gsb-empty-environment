# trigger_sets — 有理周期共同触发集合引擎

对多个**有理周期 + 相位**的触发流，在时间窗内求**共同触发集合**。全程使用
精确同余（中国剩余定理），不做浮点逐步枚举。仅依赖 Python 3.11 标准库，
测试使用 `unittest`。

## 设计要点

- **统一刻度**：把所有周期、相位、暂停边界、切换点、例外点、窗口边界的
  分母取 LCM，提升为整数同余。Python 整数任意精度，统一刻度**不溢出、
  不丢精度**（`trigger_sets/congruence.py` 的 `common_scale`）。
- **分段约束编译**：所有暂停区间端点与阶段切换点成为段边界；每段内每个流
  要么是纯同余 `t ≡ anchor (mod period)`，要么整体暂停
  （`trigger_sets/compiler.py`）。
- **惰性产出**：`Engine.iter_common` 逐段产出，段内是等差数列 + 少量例外点
  的归并；`count` / `kth` 为 O(段数 + 例外点数)，巨大窗口不逐 tick 扫描。
- **无解的算术证据**：CRT 失败时报告冲突双方的剩余、模与 gcd（剩余在
  gcd 意义下不一致即无解的算术证明）；段被暂停也会列入证据
  （`Engine.explain_no_solution`）。
- **例外触发点**：显式额外触发点，必须同时在**其他所有流**上触发
  （周期命中未暂停，或同为对方例外点）才进入共同集合；非共同例外不加入。
- **版本化规则与 checkpoint**：每次修改暂停/例外/切换都会 bump
  `stream.version`；`Checkpoint` 绑定全部流的版本号与规则内容摘要，
  查询时校验，规则变更即抛 `CheckpointMismatch`。
- **时间模型**（`trigger_sets/time_model.py`）严格区分三类概念：
  - `TimePoint`：tick 时间轴上的绝对点（精确 `Fraction`）；
  - `WallClockTime`：墙钟时刻（相对 epoch 的精确秒数）；
  - `Duration`：带单位（`tick`/`second`）的持续时长，跨单位运算抛错。
  - `OffsetTable`：合成偏移转换表，连续分段线性、有理速率，tick 与
    墙钟双向精确互转。

## 规则语义

- 基础调度：`phase + n*period`（`n >= 0`）。
- `PhaseSwitch(at, phase)`：`t >= at` 起重新锚定为 `at + phase + n*period`。
- `PauseInterval(start, end)`：半开区间 `[start, end)` 内的周期触发被抑制；
  暂停不抑制例外点。
- 例外点：显式触发时间，暂停期间依然触发。

## 目录结构

```
trigger_sets/
  time_model.py   # Duration / TimePoint / WallClockTime / OffsetTable
  congruence.py   # 统一刻度、CRT、无解证据
  streams.py      # PeriodicStream：版本化暂停、例外点、阶段切换
  compiler.py     # 分段约束编译
  engine.py       # 惰性求交：iter_common / count / kth / explain_no_solution
  checkpoint.py   # Checkpoint 绑定规则版本
tests/            # unittest 套件（33 个测试）
examples/demo.py  # 可运行样例
```

## 运行

```bash
python3.11 examples/demo.py          # 可运行样例
python3.11 -m unittest discover -v   # 测试
```

## 样例输出（`examples/demo.py` 实测）

```
=== 1. 周期2相位0 与 周期3相位1: 首交点为 4 ===
[0,20) 共同触发: ['4', '10', '16']
第0个交点: 4  计数: 3

=== 2. 版本化暂停 [4,5) 后, 下一交点变为 10 ===
A 规则版本 -> 1
[0,20) 共同触发: ['10', '16']
第0个交点: 10

=== 3. 有理周期 3/2 与 5/4: 统一刻度精确求交 ===
[0,20) 共同触发: ['3', '21/2', '18']

=== 4. 例外触发点必须与其他流相交 ===
[0,40) 共同触发: ['0', '14', '35']

=== 5. 阶段切换 (t=5 起相位变为 2) ===
[0,20) 共同触发: ['4', '7', '13', '19']

=== 6. 无解的算术证据 (周期2相位0 vs 周期2相位1) ===
no common trigger in [0, 10):
  [0, 10) crt-conflict: t ≡ 0 (mod 2) [I] conflicts with t ≡ 1 (mod 2) [J]: residues differ modulo gcd = 2

=== 7. 巨大窗口: 计数与第k个, 不逐tick扫描 ===
[0, 10^30) 共同触发计数: 1000000
第0个: 0
最后一个: 999999000099999900002378997621
惰性产出前两个: 0 , 1000000000100000000002379

=== 8. 合成偏移转换表: tick <-> 墙钟时间 ===
TimePoint(4) -> 墙钟秒 1700000004 = 2023-11-14 22:13:24+00:00
逆向转换回到: 4

=== 9. Checkpoint 绑定所有规则版本 ===
checkpoint 版本: (('M', 0), ('N', 0))  计数: 3
规则变更被检出: rule versions changed since checkpoint: (('M', 0), ('N', 0)) -> (('M', 1), ('N', 0))
```

## 测试结果（`python3.11 -m unittest discover`，实测）

```
Ran 33 tests in 0.008s
OK
```

覆盖：首交点 4 与暂停后下一交点 10、有理周期精确性（1/3 与 1/7 无漂移、
10^6 级分母统一刻度）、CRT 冲突与暂停两类无解证据、例外点相交规则
（含暂停期例外）、阶段切换重锚定、10^30 巨大窗口计数/第 k 个（公式对照）、
惰性产出、小范围穷举交叉对照（含有理暂停/例外/切换混合规则）、
checkpoint 版本绑定与变更检出、时间模型三类概念的类型隔离与偏移表往返。

# clockmap：从样本序号与校准点恢复时间

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数运算，无任何浮点）。
从**样本序号**、**名义采样率**与**校准点**恢复墙钟时间，输出**分段映射**，
不假设均匀绝对时钟。

## 模型

- **精确时间表示**：`WallClock`（墙钟时间点）与 `Duration`（持续时长）是两种
  类型，点−点=时长、点+时长=点，点+点直接 `TypeError`。构造时拒绝 `float`，
  1e18 秒附近相差 1 微秒的两个时间戳也不会被浮点吞掉。
- **校准点是误差区间而非精确点**：`Anchor(epoch, index, earliest, latest)`。
- **采样时钟斜率界**：`SlopeBounds`（秒/样本），可由名义采样率 + 漂移
  （如 100ppm）合成。任意两点间平均斜率必须落在界内（支持漂移，时钟不必
  是单一直线）。
- **分段映射**：每个 epoch 一张"合成偏移转换表"，按锚点序号分段，每段给出
  精确的仿射界限线 `t = slope*n + offset`（下界取 max、上界取 min）。
- **丢样**：序号跳 10 表示 9 个样本被丢弃，但这 10 个名义周期的时间间隔
  依然存在，被丢序号仍可映射出可行区间（`find_drops` 辅助定位缺口）。
- **epoch 不可跨接**：时钟重置产生新 epoch，序号重新计数，锚点、能带、
  转换表全部按 epoch 隔离；跨 epoch 查询直接报错。新 epoch 的墙钟时间允许
  回拨（比旧 epoch 更早），会被标记 `rollback_detected` 但不视为矛盾。
- **矛盾检测**：锚点+斜率界构成差分约束系统，用 Bellman-Ford 精确判负环。
  不一致时状态置为 `anomaly`，并给出**最小锚点冲突集**（删除式极小化：
  集合中去掉任一锚点即恢复可行）。
- **迟到校准**：`add_anchor` 可随时追加。只重算所属 epoch；先前查询结果
  作为证据（`QueryRecord`）保留，被修订的记录标记 `superseded` 并指向新值，
  构成完整审计链（`Revision.updates`）。

## 参考核验（小有理域）

`clockmap.reference` 用与主实现完全独立的暴力枚举做交叉验证：在小有理网格
上枚举各锚点时刻的所有赋值，检查全部两两斜率约束，读出查询序号的可达时间
范围，与解析结果逐一比对。丢样、回拨、近邻大时间戳场景均在核验范围内。

## 目录

```
clockmap/
  timetypes.py   # WallClock / Duration，精确有理数，拒绝 float
  model.py       # Anchor / SlopeBounds / Line / Bound / Segment
  mapper.py      # 分段映射、可行性、最小冲突集、迟到修订与证据
  reference.py   # 小有理域暴力枚举参考核验器
examples/demo.py # 运行样例
tests/           # unittest 测试（33 个）
```

## 运行

```bash
python3.11 examples/demo.py
python3.11 -m unittest discover -s tests -v
```

## 样例输出（节选）

```
=== 1. piecewise mapping from indices + calibration intervals ===
  synthetic offset conversion table, epoch 0:
    n in [None, 0]: t >= 11/100*n + (100) ; t <= 9/100*n + (101)
    n in [0, 100]: t >= 9/100*n + (100), t >= 11/100*n + (99) ; t <= 9/100*n + (102), t <= 11/100*n + (101)
    n in [100, None]: t >= 9/100*n + (101) ; t <= 11/100*n + (100)
  map(0, 50) : [209/2, 213/2]  (width 2 s, feasible=True)

=== 3. late calibration revises the mapping, evidence is kept ===
  revision v3: feasible=True, revised 4 earlier result(s)
    map(0, 50): [209/2, 213/2] -> [105, 106] (old kept as evidence)

=== 5. inconsistent calibration -> anomaly + minimal conflict set ===
  status(0) = 'anomaly' after anchor 'bad'
  minimal conflict set: ['late-ntp', 'bad']

=== 6. huge nearby timestamps stay exact (no float swallowing) ===
  band at n=50: [1000000000000000004500001/1000000, 500000000000000002750001/500000]
  exact width: 1000001/1000000 s; as float both ends are 1e+18
```

完整输出见 `examples/demo.py` 的实际运行。

## 测试结果

```
$ python3.11 -m unittest discover -s tests
.................................
----------------------------------------------------------------------
Ran 33 tests in ~7s

OK
```

覆盖：类型区分与精确性（8）、映射核心：能带/转换表/丢样/冲突/epoch/迟到
修订（15）、参考核验交叉比对（5）、端到端场景：丢样跳 10、回拨、1e18 秒
附近微秒级区分、校准不一致标异常（5）。

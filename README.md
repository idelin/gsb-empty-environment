# 分段匀速轴对齐盒碰撞检测

Python 3.11，仅标准库。坐标、速度、时间一律为 `int` 或 `fractions.Fraction`，
全程精确有理数运算，**不使用任何 epsilon 容差**。

## 运行

```bash
python3.11 demo.py                  # 可运行样例
python3.11 -m unittest test_collision -v   # 测试
```

## 显式规则（退化情形的定义）

- **碰撞**：两盒在所有坐标轴上严格重叠（每轴交集为正长度开区间），即正体积重叠。
- **瞬时相切**：仅在孤立时刻接触（交集退化为点）不构成碰撞。
- **持续贴面**：某轴交集恒为单点（贴面滑动）同样不构成碰撞。
- **初始重叠**：窗口起点已重叠时，碰撞区间从窗口起点开始，首碰撞时刻为 `t0`。
- **静止分离**：相对速度为零且分离的物体永不碰撞（解析上自然成立）。
- **零长度窗口**（`t1 <= t0`）：不产生任何碰撞。
- **区间合并**：跨段边界的相邻重叠区间在报告时合并为 `(start, end)`；
  合并点（段边界）处的接触可能是瞬时的，重叠集是开区间语义。
- **盒尺寸**：每轴必须严格为正（点盒不予支持，避免用 epsilon 模拟）。
- **销毁**：对象在 `[birth, alive_until)` 内存在，销毁时刻及之后不参与碰撞。

## 结构

- `trajectory.py` — 分段匀速轨迹（连续、按段起点递增），`Fraction` 精确。
- `narrowphase.py` — 窄阶段：断点扫描 + 每段线性不等式精确求解碰撞区间。
- `broadphase.py` — 宽阶段：时间窗扫掠时空包围盒索引，x 轴 sweep-and-prune 产候选对。
- `world.py` — 对象管理、快照查询、事件驱动模拟。
- `reference.py` — 参考实现（全对 × 段笛卡尔积朴素解析），供对照测试。
- `test_collision.py` — unittest 套件。

## 事件模型

- 每次轨迹修改（`change_velocity` / `set_trajectory` / `destroy`）递增对象版本号。
- 预测事件携带双方版本号；弹出时版本不匹配即为旧预测，直接丢弃
  （例：A、B 预测 t=5 碰撞，t=3 时 A 转向，t=5 的旧事件不会生效）。
- 同一时刻的全部有效事件作为**同一批**，基于批处理前的前态计算碰撞信息，
  一次性交给 `respond(world, t, collisions)` 回调；三物体同刻碰撞不会因
  处理顺序遗漏。
- 批处理后：轨迹被修改的对象立即重预测其所有候选对；未被修改的碰撞对
  从该时刻之后（不含本次区间）重预测下一次碰撞。
- `simulate(..., max_events=N)`：处理的有效事件数超过预算时抛出
  `EventBudgetExceeded`，用于终止零时间环（响应在同一时刻反复制造碰撞）。

## API 摘要

```python
w = World()
w.add_object("A", pos=(0, 0), size=(1, 1), vel=(1, 0), start=0)
w.change_velocity("A", (0, 1), t=3)        # 变速
w.set_trajectory("A", [(t, pos, vel)])     # 轨迹替换
w.destroy("A", t=8)                        # 销毁

w.collision_intervals(t0, t1)   # {pair: [(start, end), ...]}，快照查询不改状态
w.first_collision(t0, t1)       # (time, [pairs]) 或 None
w.snapshot(t)                   # t 时刻处于重叠状态的对象对集合
w.simulate(t0, t1, respond=cb, max_events=256)  # [(t, [Collision, ...]), ...]
```

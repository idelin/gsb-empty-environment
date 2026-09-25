# arcsample — 多段折线与共享接缝的精确等弧长采样

纯标准库（Python 3.11+，`fractions.Fraction` 全程精确有理运算，无浮点、无 epsilon）。
坐标只接受整数或 `Fraction`。

## 结构

- `arcsample/rational.py` — 有理数工具：精确平方根、精确欧氏距离。
- `arcsample/model.py` — `Path`（多子路径、`BREAK` 断点、闭合）、顶点/段/里程表。
- `arcsample/sampling.py` — 等弧长采样、预算游标、`PathSampler.move_point` 局部修复与可逆 `Patch`。
- `arcsample/seam.py` — `SeamSampler`：共享边规范缓存，反向遍历得逆序同点。
- `tests/test_arcsample.py` — unittest 套件（17 个用例）。

## 核心语义

**有理里程**：跨段累计里程是 `Fraction` 精确累加（1/3+1/3+1/3 == 1）。
段长必须是有理数（轴对齐或 3-4-5 类勾股边）；长度为 √2 之类的段在构造时抛出
`NonRationalLengthError`——这是定义的退化行为，绝不静默舍入。

**样本身份（ID 稳定映射）**：
- 关键点样本：`<path>:<vertex id>`，锚定在顶点上；
- 常规样本：`<path>:g<k>`，锚定在子路径起点（闭合路径的固定起点）出发的第 k 个间距网格。
局部修改后，下游样本 ID 不变，只有里程/位置随补丁移动。

**局部修复**：`move_point` 返回可逆 `Patch`（added/removed/moved + 受影响里程区间 +
长度变化）。补丁只触及受影响弧长范围（及总长度变化引起的尾部里程平移）；
`patch.apply(旧样本) == 独立全量重采样`，`patch.inverse()` 撤销。

**预算游标**：`sample(budget=N)` 预算不足时返回 `complete=False` 与可续 `Cursor`
（携带路径版本号，过期游标报错），绝不静默减点。

**共享接缝**：`SeamSampler` 以规范（无向）边为键缓存精确采样；反向遍历复用同一缓存，
输出严格为逆序同点（`t→1-t`、方向取反、里程 `L-m`），多条折线共享边逐点一致。

## 退化情形定义

- 零长段：不产生样本、不推进里程、插值永不跨越它；重合顶点各自保留为关键点。
- 断点 `BREAK`：拆分为独立子路径，里程各自从 0 开始，无跨断点插值。
- 单点子路径：恰好一个关键点样本，方向为 `None`（无定义）。
- 闭合路径：固定起点为接缝（`seam=True`），周长恰好是间距倍数时终点不重复采样。
- 间距 ≤ 0、负预算：抛 `ValueError`。

## 运行

```
python3.11 -m unittest discover -s tests -v
```

实际运行结果见 `RESULTS.md`。

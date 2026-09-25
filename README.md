# clockalign：分段仿射时间校正

根据本地与参考时间锚点拟合分段仿射映射，支持锚点误差区间与异常剔除，
输出校正值及不确定范围。纯 Python 3.11 标准库实现（`fractions.Fraction`
精确有理数运算，无浮点误差），测试基于 `unittest`。

## 模型

每段映射形如 `y = alpha_i * x + beta_i`：

- `x`：本地墙钟读数（秒，有理数）；`y`：参考时间点（秒，有理数）；
- `alpha_i`：漂移斜率，受声明的最大漂移斜率约束 `rate_min <= alpha_i <= rate_max`；
- 每个锚点给出参考值误差区间 `[ref - minus, ref + plus]`（可非对称），
  可行映射必须穿过所有保留锚点的区间；
- 分段交界按声明处理：`continuous=True` 时显式满足连续性等式
  `alpha_l*b + beta_l = alpha_r*b + beta_r`；`continuous=False` 时允许
  声明跳变，跳变量受 `max_jump` 约束。

## 时间类型（严格区分）

- `WallTime`：本地墙钟读数，只有相对意义（可相减得 `Duration`、可加减 `Duration`）；
- `Instant`：参考时间轴上的绝对时间点（相对参考纪元）；
- `Duration`：持续时长（可为负）。

三者不可混用（混合比较/运算抛 `TypeError`）；内部一律以秒的有理数存储，
拒绝 `float` 输入（请用 `int` / `Fraction` / 十进制字符串，如 `"0.05"`）。

## 异常剔除：最少删锚点（非阈值式）

`reconcile(model, anchors, budget=None)` 做精确搜索：

- 可行性由精确有理 LP（两阶段单纯形法，Bland 规则）判定；
- 自预算 0 起逐级加深，分支由**极小冲突核**指导（核中至少删一个），
  第一个产生可行方案的预算即**最少删除数**；
- **最优性证据**：上一级预算的搜索树全部叶节点不可行 + 本级可行方案数，
  连同搜索轨迹、冲突核一起输出；
- **预算未决分开**：`budget=B` 时若 B 级内不可行，返回
  `status="undecided_within_budget"`，与最优结果明确区分；
- 同一本地时间对应不相交参考区间天然构成冲突，由冲突核精确指认
  （如 `[P1, P1']`），不做任何启发式阈值特判。

## 查询：上下界由所有可行映射推出

`correct(recon, local)` 对每个最优方案（最少删除的可行映射族）分别求
min/max 精确 LP，上下界取并集，输出 `Correction(lower, upper, mid,
uncertainty)`。规则：

- 外推超过允许距离（`extrapolation_margin`）拒绝（`ExtrapolationError`）；
- 查询点落在声明不连续的交界上必须显式给出 `side="left"/"right"`
  （否则 `JunctionSideError`）；
- `cross_check=True` 时，小规模系统用**有理约束枚举**（穷举顶点）逐点
  对照 LP 结果（`clockalign/enumcheck.py`）。

## 目录结构

- `clockalign/time_model.py` — 精确时间模型（WallTime / Instant / Duration）
- `clockalign/anchors.py` — 带误差区间的锚点
- `clockalign/model.py` — 分段模型声明（斜率界、交界连续性/跳变）
- `clockalign/system.py` — 编译为有理线性约束系统
- `clockalign/lp.py` — 精确有理两阶段单纯形法 + 极小冲突核收缩
- `clockalign/enumcheck.py` — 有理约束枚举对照器（穷举顶点）
- `clockalign/reconcile.py` — 最少删除精确搜索 + 查询界
- `clockalign/synthetic.py` — 合成偏移转换表（确定性，带真值）
- `demo.py` — 运行样例；`tests/` — unittest 测试

## 运行

```bash
python3.11 demo.py                          # 运行样例
python3.11 -m unittest discover -s tests -v # 运行测试
```

## 运行样例（节选）

样例 1：合成偏移转换表（3 段连续、14 锚点 + 2 异常、误差 ±0.05s、
最大漂移斜率 ±0.001、外推允许 200s）：

```
状态: optimal, 最少删除数: 2
最优性证据: 预算 1 的搜索树（3 个节点）全部叶节点不可行，故最少删除数 >= 2；
           预算 2 找到 1 个可行方案，故最少删除数 = 2。
冲突核: [14, 15] -> ['X00(异常)', 'X01(异常)']  等
最优方案: 删除 [14, 15]
  段0: alpha=5001/5000 (~1.000200), beta=999/20
  段1: alpha=9997/10000 (~0.999700), beta=1009/20
  段2: alpha=10001/10000 (~1.000100), beta=197/4
查询:
  T+1000s -> [R+1050.144737s, R+1050.255263s]  真值=1050.200000 覆盖=True
  T+-200s -> [R+-150.111053s, R+-149.968947s]  真值=-150.040000 覆盖=True
  T+-201s -> 拒绝: 超出外推允许距离（±200）
  T+4201s -> 拒绝: 超出外推允许距离（±200）
```

样例 2：预算 1 < 最少删除数 2 -> `undecided_within_budget`（与最优分开报告）。

样例 3：同一本地时间不同参考值 -> 冲突核 `['P1', "P1'"]`，最少删除数 1。

样例 4：声明跳变（<=0.5s）0 删除可行；交界点查询必须指明侧；
强制连续则最少删除数 2。

样例 5：有理约束枚举对照，逐点与单纯形 LP 结果一致。

## 测试结果

```
$ python3.11 -m unittest discover -s tests
......................................
----------------------------------------------------------------------
Ran 38 tests in ~9s

OK
```

覆盖：时间类型区分与精确运算、LP 求解器（含自由变量/负右端/冗余等式/
无界）、合成表端到端（最少删除=2 且恰好剔除注入异常、代表映射满足斜率
界与交界连续性等式、查询界覆盖真值、外推拒绝、预算未决）、同本地时间
冲突、声明跳变与交界侧查询、非对称误差区间、枚举对照一致性。

# wmmf — 加权最大最小公平带宽分配

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数运算，不调用任何求解器）。
对共享链路上的多个带权、带上限需求做加权最大最小公平（weighted max-min fair）分配，
输出冻结顺序与瓶颈链路，支持链路故障 / 需求撤回后的增量再分配，并给出可独立核验的
公平性证书与小有理网格穷举核验。

## 模型与保证

- 每个需求有权重 `weight`、上限 `cap`、路由（链路集合，可由多条路径合并而成）。
- **多路径不重复计费**：路由是多条路径的并集，速率在每条链路上只计一次，
  共享链路的多路径需求不会在每链路上重复获配。
- **已达上限者不再分配**：注水过程中达到 `cap` 的需求立即冻结，剩余容量继续分给他人。
- **容量不超限**：所有结果经 `check_feasible` 校验（速率非负、不超上限、链路负载不超容量）。
- **增量再分配**：链路故障（容量归零）、容量调整、需求撤回、新增需求都会触发
  完全解冻重算（`Reallocator`），而不是在旧的剩余量上继续扣减——饱和集合变更时
  原先被冻结的需求允许解冻回升（见下方样例中 B 从 2 升到 4）。

## 公平性证书

对每个未满足需求 d（速率 < 上限），证书给出一条瓶颈链路 l 满足：

1. l 已饱和（负载 == 容量）；
2. d 在 l 上持有最大归一速率（`rate/weight` 不小于 l 上任何其他需求）。

因此**提高任一未满足需求，必然降低某个归一速率不更高者**：l 已满，d 的增量只能从
l 上归一速率 ≤ d 的需求那里挤出来。`verify_certificate` 独立于分配器重新计算负载
并逐条核验。已达上限的需求视为已满足，无需证人。

## 网格穷举核验

`gridverify.verify_optimal` 在小有理网格 `{0, step, 2·step, …, cap}` 上穷举所有可行
分配，按归一速率向量的字典序（lex-max）与候选分配比较。三种结论严格分开：

- `OPTIMAL`：可行且网格上无严格更优者；
- `INFEASIBLE` / `NOT_OPTIMAL`：违反约束 / 网格上存在更优点（附反例）；
- `BUDGET_UNDECIDED`：枚举预算耗尽，最优性未决（可行性已先行判定）。

`suggest_step` 取所有上限与候选速率分母的最小公倍数，保证候选点落在网格上。

## 运行

```bash
python3.11 -m wmmf.demo        # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 运行样例（`python3.11 -m wmmf.demo` 节选）

初始网络：L1=10, L2=4, L3=9, L4=5；A:{L1}, B:{L1,L2}, C:{L2}（w=1, cap=100），
D:w=2, cap=6，路径 [[L3,L4],[L4]]（L4 只计一次）。

```
== initial weighted max-min fair allocation ==
  A: rate=   8 normalized=   8 cap=100 frozen-by=link L1
  B: rate=   2 normalized=   2 cap=100 frozen-by=link L2
  C: rate=   2 normalized=   2 cap=100 frozen-by=link L2
  D: rate=   5 normalized= 5/2 cap=6 frozen-by=link L4
  freeze order:
    round 0: level=2 frozen=['B', 'C'] bottlenecks=['L2'] capped=[]
    round 1: level=5/2 frozen=['D'] bottlenecks=['L4'] capped=[]
    round 2: level=8 frozen=['A'] bottlenecks=['L1'] capped=[]
== fairness certificate (verified=True) ==
  B: unsatisfied; bottleneck link L2 is saturated and B holds the max normalized
     rate 2 on it -> raising B forces down a demand with normalized rate <= 2
  ...
== exhaustive grid verification ==
  grid step: 1, nodes visited: 1072
  status: optimal
```

增量再分配（饱和集合变更 → 完全解冻重算，而非继续扣剩余量）：

```
== incremental: demand C withdraws (saturated set changes -> unfreeze) ==
  A: 8 -> 6
  B: 2 -> 4        # 原先被 L2 冻结的 B 解冻回升；只扣剩余量会让 B 永远停在 2
== incremental: link L2 fails (capacity -> 0) ==
  A: 6 -> 10
  B: 4 -> 0
== incremental: link L2 partially recovers (capacity -> 3) ==
  A: 10 -> 7
  B: 0 -> 3
```

上限生效（已达上限者不继续分配）：

```
== demand at its cap stops; the rest fills on ==
  A: rate=   3 normalized=   3 cap=3 frozen-by=cap
  B: rate=   7 normalized=   7 cap=100 frozen-by=link L1
```

## 测试结果

`python3.11 -m unittest discover -s tests`（Python 3.11）：

```
Ran 28 tests in 0.206s

OK
```

覆盖：注水分配（权重 / 上限 / 冻结顺序 / 瓶颈链路）、多路径不重复计费、
有理数精确性、容量与上限约束、证书构造与独立核验、篡改分配拒绝、
证书性质网格暴力验证、增量再分配（撤回解冻 / 故障 / 恢复 / 新增）、
网格穷举核验（最优 / 不可行 / 非最优 / 预算未决）。

## 代码结构

- `wmmf/model.py` — 需求 / 网络数据模型与校验，链路负载（每链路计一次）
- `wmmf/allocator.py` — 注水式加权最大最小公平分配，输出冻结顺序与瓶颈链路
- `wmmf/certificate.py` — 公平性证书的构造与独立核验，可行性检查
- `wmmf/reallocator.py` — 故障 / 撤回 / 调整后的增量再分配（完全解冻重算）
- `wmmf/gridverify.py` — 小有理网格穷举核验（含预算上限）
- `wmmf/demo.py` — 运行样例
- `tests/` — 28 个 unittest 用例

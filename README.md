# cardenc — 基数约束的 CNF 编码（Python 3.11，仅标准库）

将 **至多 k / 至少 k / 恰好 k** 基数约束编码为 CNF，提供两种编码方式、
投影等价性穷举验证、传播能力对比、增量收紧与基于假设（assumptions）的撤回。
不调用任何 SAT/SMT 库（求解器为内置的小型 DPLL 实现）。

## 目录结构

- `cardenc/core.py` — `VarPool`（变量编号全局唯一、不冲突）、`CNF`、单位传播、DPLL 求解器（支持假设）
- `cardenc/encodings.py` — 顺序计数器（Sinz）与排序网络（Batcher 奇偶归并）编码、边界简化、`check_direct` 独立检查器
- `cardenc/incremental.py` — `IncrementalAtMost`：容量 K 结构只建一次，界 k 的子句由激活文字守卫，收紧只加子句，撤回靠切换假设
- `cardenc/verify.py` — 投影等价穷举验证、`lift` 原赋值→辅助赋值见证、传播能力（GAC）分析、逐投影模型计数
- `tests/test_cardenc.py` — unittest 测试套件
- `demo.py` — 运行样例

## 快速开始

```bash
python3.11 demo.py                  # 运行样例
python3.11 -m unittest discover -s tests   # 运行测试
```

```python
from cardenc import VarPool, encode, solve, check_direct, ATMOST

pool = VarPool()
x = pool.reserve(5, "x")                    # 原变量 1..5
enc = encode(pool, x, ATMOST, 2, method="seq")   # 或 method="net"
model = solve(enc.cnf)                      # 内置 DPLL
assert check_direct(ATMOST, 2, [model[v] for v in x])   # 独立检查器验证原约束

# 原赋值 -> 辅助赋值见证（投影等价 "=>" 方向）
values = {v: model[v] for v in x}
witness = dict(values); witness.update(enc.lift(values))
assert enc.cnf.evaluate(witness) is True
```

## 边界语义（k<0 / k>n 按语义判定，不建编码）

| 约束 | 平凡真 | 平凡假 | 化简为单位子句 |
|---|---|---|---|
| at-most k | k >= n | k < 0 | k == 0 → `¬x_i` |
| at-least k | k <= 0 | k > n | k == n → `x_i` |
| exactly k | n = 0 且 k = 0（空 CNF，可满足） | k < 0 或 k > n | k = 0 / k = n |

## 增量收紧与撤回

`IncrementalAtMost` 先按容量 K 建一次顺序计数器结构；每个界 k 注册激活
文字 `a_k`，界相关子句形如 `(¬a_k ∨ ¬x_i ∨ ¬s_{i-1,k})`。收紧到更小的 k
只新增子句；撤回（放松）时只需在求解假设中换回较弱界的激活文字，无需
删除任何子句。辅助变量全部由共享 `VarPool` 分配，命名与编号均不冲突。

## 运行样例（`python3.11 demo.py` 实际输出）

```
== Encoding sizes (n=8, k=3) ==
  atmost   seq  status=encoded  clauses=  50 aux= 24
  atmost   net  status=encoded  clauses= 115 aux= 38
  atleast  seq  status=encoded  clauses=  78 aux= 40
  atleast  net  status=encoded  clauses= 115 aux= 38
  exactly  seq  status=encoded  clauses= 128 aux= 64
  exactly  net  status=encoded  clauses= 116 aux= 38
== Projection equivalence (exhaustive, n<=5, k in -1..n+1) ==
  198 encodings verified over 2682 original assignments, mismatches: 0
== Exhaustive auxiliary enumeration (n=4, at-most 2) ==
  seq: aux=8, 16 original assignments, 11 with >=1 extension, total CNF models 51
  net: aux=10, 16 original assignments, 11 with >=1 extension, total CNF models 11
== Propagation strength (n=6, k=2) ==
  atmost   seq  clauses=  26 aux= 12 GAC=True conflict_on_violation=True avg_propagated=6.9 trials=35
  atmost   net  clauses= 116 aux= 39 GAC=True conflict_on_violation=True avg_propagated=18.4 trials=35
  exactly  seq  clauses=  72 aux= 36 GAC=True conflict_on_violation=True avg_propagated=20.9 trials=56
  exactly  net  clauses= 117 aux= 39 GAC=True conflict_on_violation=True avg_propagated=22.5 trials=56
== Incremental tightening / retraction via assumptions ==
  bound<=2 : model=[True, False, False, False, False] independent_check=True
  bound<=1 : model=[True, False, False, False, False] independent_check=True
  bound<=1 with x1,x2 forced: UNSAT (as expected)
  retract to <=2 with x1,x2: model=[True, True, False, False, False] independent_check=True
== Boundary semantics ==
  atmost   k=-1 n=3: status=trivial-false  clauses=1
  atmost   k= 5 n=3: status=trivial-true   clauses=0
  atmost   k= 0 n=3: status=simplified     clauses=3
  atleast  k=-1 n=3: status=trivial-true   clauses=0
  atleast  k= 5 n=3: status=trivial-false  clauses=1
  atleast  k= 3 n=3: status=simplified     clauses=3
  exactly  k= 0 n=0: status=trivial-true   clauses=0
  exactly  k= 2 n=0: status=trivial-false  clauses=1
```

## 两种编码的差异（样例解读）

- **子句/辅助变量规模**：顺序计数器为 O(n·k)，排序网络为 O(n·log²n)；
  k 很小时顺序计数器更省（n=8,k=3：50 子句 vs 115），恰好 k 时网络编码
  可共享一个排序器（116 子句 vs 顺序计数器两个计数器共 128）。
- **传播能力**：两者对 at-most / at-least / exactly 均达到 GAC（任意 k 个
  变量置真后单位传播推出其余原变量，且 k+1 个置真即冲突）；网络编码平均
  每次传播推出的赋值更多（18.4 vs 6.9），因为比较器同时编码两个输出。
- **模型计数**：n=4, at-most 2 时网络编码每个可满足原赋值恰有 1 个辅助
  扩展（函数式），顺序计数器有多个（51 vs 11 个总模型），因部分辅助变量
  自由。

## 测试结果

```
$ python3.11 -m unittest discover -s tests
.................
----------------------------------------------------------------------
Ran 17 tests in 68.159s

OK
```

覆盖：边界语义（k<0、k>n、n=0 恰好 0）、n≤5 全部 k 的投影等价穷举
（与直接计数一致）、lift 见证有效性、变量编号不冲突、子句数公式、
GAC 传播能力、独立检查器、增量收紧/撤回。

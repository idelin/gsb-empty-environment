# setsolver — 有限域集合变量约束求解器（Python 3.11，仅标准库）

## 模型

每个集合变量维护：

- `lb`：必含下界（一定在集合中的元素）
- `ub`：可能上界（可能在集合中的元素）
- `min_card` / `max_card`：基数上下界

不变式：`lb ⊆ ub`、`|lb| ≤ max_card`、`min_card ≤ |ub|`、`min_card ≤ max_card`。
传播一旦发现下界不含于上界（或基数矛盾）立即抛出 `Conflict`。

## 约束与传播规则

- `Subset(a, b)`（a ⊆ b）：b 继承 a 的下界；a 的上界被 b 的上界裁剪；基数沿包含方向传播。
- `Union(c, a, b)`（c = a ∪ b）：`lb(c) ⊇ lb(a) ∪ lb(b)`；`ub(c) ⊆ ub(a) ∪ ub(b)`；
  c 中必含元素若在某操作数中不可能，则被强制进入另一操作数的下界。
- `Intersection(c, a, b)`（c = a ∩ b）：**下界与上界分别计算**——
  `lb(c) ⊇ lb(a) ∩ lb(b)`（仅在两边都必含才必含），`ub(c) ⊆ ub(a) ∩ ub(b)`；
  在一边必含而在另一边不可能的元素从 c 的上界剔除。
  仅在两边都"可能"的元素**不会**被当作必含。
- 基数传播：`|ub| = min_card` 时全部可能元素转为必含；`|lb| = max_card` 时上界收缩为下界；
  并/交/子集各自附带健全的基数不等式（如 `|a∩b| ≥ min_a + min_b − |ub(a) ∪ ub(b)|`）。

传播规则单调（lb 只增、ub 只减、界只紧），有限域上不动点循环必然终止。
不调用任何 SAT/SMT 库：传播到不动点后，对未决元素做朴素回溯搜索（包含/排除二分支）。

## 独立检查器

`check_solution(solver, assignment)` 不复用任何传播内部状态，直接依据
**声明时**的约束（初始必含/可能集合、声明的基数界、各约束语义）重新验证解。
`Solver.solve()` 在声称有解前必定通过该检查器验原约束，否则抛出内部错误。

## 运行

```bash
python3 examples.py        # 运行样例
python3 -m unittest test_setsolver -v   # 运行测试
```

## 样例输出（实测）

```
例 1：必含 3 个元素但最大基数为 2 —— 立即冲突
检测到冲突: X: lower bound has 3 required elements but max cardinality is 2
solve() 返回: None

例 2：交集的下界与上界分别计算；可能成员 ≠ 必含成员
A: lb=[1], ub=[1, 2, 3]
B: lb=[2], ub=[1, 2]
C = A ∩ B 传播后: lb=[], ub=[1, 2]

例 3：并集 + 子集 + 基数约束，求解并用独立检查器验证
传播后 Y: lb=[2], ub=[1, 2, 3, 4]   # 2 在 Z 必含、在 X 不可能 => 被迫进入 Y
solve() 得到解: {'X': {1, 3}, 'Y': {1, 2}, 'Z': {1, 2, 3}}
独立检查器验证原约束: True
篡改后的解 {... 'Y': {1, 2, 3} ...} 被检查器接受? False

例 4：交集下界传播引发冲突（C 必含 1 但 max_card(C)=0）
检测到冲突: C: required lower bound [1] is not a subset of possible upper bound []
solve() 返回: None
```

## 测试结果（实测）

```
Ran 20 tests in 0.001s
OK
```

覆盖：下界⊄上界冲突、必含 3 元素但 max_card=2 冲突、min_card 超过可能元素数、
基数收紧规则、子集 lb/ub/基数双向传播、并集下界/上界与反向强制、
交集 lb 与 ub 分别计算、可能成员不误判为必含、交集引发的冲突、
求解+独立检查器验证、检查器拒绝篡改解、unsat 返回 None。

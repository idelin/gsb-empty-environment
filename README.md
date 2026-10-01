# symbreak：可验证对称性破缺（纯 Python 3.11 标准库）

对**显式声明**的可交换变量组生成字典序代表（lex-representative）约束，
保证每个等价类（轨道）至少保留一个解，并提供从代表元**恢复全部对称解**的方法。
不调用任何 SAT/SMT 库，全部基于有限域上的回溯枚举；测试使用 `unittest`。

## 设计要点

- **先证明，后破缺**：用户给定的置换必须先通过 `verify_automorphism`
  验证确为原始约束与目标的自同构（在有限域上精确枚举检查），
  才会生成任何字典序约束；验证失败则不添加任何约束（原子性）。
- **最小反例**：错误置换返回字典序最小的不保持约束/目标的赋值，
  并指出具体是哪条约束（或目标）不被保持。
- **特殊角色保护**：`Problem.special` 中的变量一旦被置换移动即拒绝。
- **声明校验**：可交换组内变量域必须一致、约束必须对称，否则
  `declare_exchangeable` 抛出 `SymmetryRejected` 并携带反例。
- **预算闭包**：`closure` 按预算 BFS 构造生成元闭包，超预算抛
  `BudgetExceeded`。
- **只动已声明变量**：未声明对称的变量绝不出现在任何排序/字典序约束中。
- **独立检查器**：`check_original` 只对照原始约束（不含破缺约束）
  验证求解器声称的每个解。
- **小问题穷举验证**：`verify_small` 枚举全部原始解、计算轨道，验证
  (1) 保留解全部通过独立检查；(2) 每个轨道至少保留一解（不丢类）；
  (3) 每个被删解都能由保留代表元经群元素恢复。

## 文件结构

- `symbreak/core.py` — CSP 模型（变量、有限域、约束、目标、特殊角色）
- `symbreak/permutations.py` — 置换运算与自同构验证（含最小反例）
- `symbreak/groups.py` — 预算内的生成元闭包
- `symbreak/symmetry.py` — 对称声明与字典序约束生成
- `symbreak/solver.py` — 回溯求解器 + 独立检查器
- `symbreak/recovery.py` — 对称解恢复
- `symbreak/verify.py` — 小问题轨道穷举验证
- `demo.py` — 运行样例；`tests/test_symbreak.py` — 23 个单元测试

## 用法示例

```python
from symbreak import Constraint, Problem, declare_exchangeable, verify_small

problem = Problem(
    domains={v: (0, 1, 2) for v in "abcd"},
    constraints=[Constraint("sum==3", tuple("abcd"),
                            lambda a, b, c, d: a + b + c + d == 3)],
)
added, group = declare_exchangeable(problem, list("abcd"))  # 先验证再破缺
report = verify_small(problem, group)                       # 穷举验证
assert report.ok
```

一般生成元（如旋转）用 `add_symmetry_generators(problem, [perm], budget=...)`，
对每个非单位闭包元素添加 `X <=lex pi(X)` 约束；被删解用
`recover_symmetric(retained, group)` 恢复。

## 运行样例（`python3.11 demo.py` 实际输出节选）

```
Sample 1: fully exchangeable group -> ordering constraints + recovery
added constraints    : ['lex_order[a<=b]', 'lex_order[b<=c]', 'lex_order[c<=d]']
group size (S_4)     : 24
solutions (original): 16
solutions (retained): 2
orbits: 2, covered: 2
retained all valid (independent checker): True
deleted all recoverable: True
VERDICT: OK
recovered solutions  : 16 (== original count: 16 )

Sample 2: wrong permutation -> minimal counterexample
counterexample: constraint 'x<=y' is not preserved: at assignment
{'x': 0, 'y': 1} it evaluates to True but to False after applying the permutation

Sample 3: rotation generator, budget, objective preservation
objective 'x1-only' rejected: objective 'x1-only' is not preserved: at assignment
{'x1': 0, 'x2': 0, 'x3': 1} it evaluates to 0 but to 1 after applying the permutation
rotation accepted; closure size: 3
added lex constraints: ['lex_le[x1,x2,x3<=x2,x3,x1]', 'lex_le[x1,x2,x3<=x3,x1,x2]']
budget respected: group closure exceeded budget 2 (already reached 2 distinct elements)

Sample 4: declarations that must be rejected
domain mismatch rejected: variable 'p' (domain (0, 1)) maps to 'q' with a different domain
special role rejected  : special-role variable 'captain' is moved to 'mate'; ...
constraint mismatch    : constraint 'u<=v' is not preserved: at assignment {'u': 0, 'v': 1} ...

Sample 5: tiny domains, exhaustive orbit check (no class lost)
solutions (original): 7 / retained: 3 / orbits: 3, covered: 3 / VERDICT: OK
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
...
Ran 23 tests in 0.007s

OK
```

覆盖：自同构验证（合法/错误置换、最小反例、特殊角色、域不一致、目标不保持）、
闭包与预算、字典序约束只作用于已声明变量、拒绝后零副作用、求解器与朴素枚举一致、
独立检查器、每轨道至少一解、被删解可恢复、小域穷举不丢类。

# Integer Linear Bound Propagation (Python 3.11, stdlib only)

对整数系数线性不等式做区间界传播（bound propagation），队列驱动至固定点，
带解释记录、约束撤回与域恢复、模同余必要条件检查。不使用任何 SAT/SMT 库。

## 文件

- `solver.py` — 核心求解器（传播队列、解释、checkpoint/backtrack、retract、分支搜索）
- `checker.py` — 独立检查器 + 精确枚举参考实现（测试预言机）
- `demo.py` — 可运行样例（`python3.11 demo.py`）
- `test_solver.py` — unittest 套件（`python3.11 -m unittest -v`）

## 设计要点

- **传播规则**：每条约束规范化为 `sum(a_i x_i) <= rhs`。对每个变量：
  `a_j x_j <= rhs - rest_min`，其中 `rest_min` 为其余项的最小可达和。
  - `a_j > 0`：`x_j <= floor(slack / a_j)`（Python `//` 即向下取整）
  - `a_j < 0`：`x_j >= ceil(slack / a_j)`（`ceil_div`，符号翻转后向上取整）
- **等式拆双向**：`==` 同时生成 `<=` 与 `>=` 两个传播形式。
- **模同余必要条件**：等式要求 `gcd(|a_i|)` 整除 rhs，否则立即判无解。
  这能发现区间传播漏掉的无解（如 `2x+2y+2z==3` 在 `[0,1]^3` 上区间不动、
  但 gcd=2 不整除 3）。**这不构成完整求解**：`x+y==1, x-y==0` 在 `[0,1]`
  上传播与 gcd 都静默，但实际无整数解——传播只保证 sound，不保证 complete。
- **大整数**：全部使用 Python 任意精度 int，10^50 级系数无溢出。
- **队列固定点**：变量域收缩时唤醒所有含该变量的约束，队列排空即固定点。
- **解释**：每次剪枝记录 `{constraint, var, kind, old, new, detail}`，
  存于 `solver.explanations` / `solver.prune_log`，冲突异常也携带解释。
- **撤回与恢复**：`retract(cid)` 停用约束后从初始域重放剩余约束至固定点；
  `checkpoint()/backtrack(cp)` 提供 trail 式撤销（搜索分支使用）。
- **完备性边界**：`propagate()` 只报可靠矛盾；`solve()` 在有限域上做
  完备分支搜索，返回的解由 `checker.check_assignment` 对**原始约束**独立验证。

## 必含示例（demo.py 输出摘要）

```
2x <= 3            -> x ∈ (-10, 1)     # floor(3/2) = 1
-2x <= 3           -> x ∈ (-1, 10)     # ceil(3/-2) = -1
0*x + y <= 5       -> x 不变, y ∈ (-10, 5)
0 <= -1            -> Contradiction（常量矛盾立即失败）
x + y == 4         -> x,y ∈ [0,4]（等式双向传播）
2x+2y+2z == 3      -> Contradiction（gcd=2 ∤ 3，区间传播本来发现不了）
10^50 x <= 3·10^50+7 -> x <= 3（大整数无溢出）
```

## 测试

`python3.11 -m unittest -v`：25 个测试全部通过，包括：

- 负系数两个方向的 ceil/floor 取整（含整除边界 off-by-one）
- 大整数系数/界/乘积（10^40–10^60）
- 共享变量队列传播至固定点、解释内容、撤回后域恢复、checkpoint 撤销
- 随机交叉验证（300+150 个随机实例）：**每次剪枝**都与精确枚举对照——
  被剪掉的值不得出现在任何精确解中；传播报矛盾 ⇨ 枚举必须为空；
  `solve()` 的可满足/不可满足判定与枚举完全一致，解通过独立检查器。

# intprop — 整数线性不等式界传播求解器

纯 Python 3.11+ 标准库实现，不调用任何 SAT/SMT 库。变量域有限。

## 文件

- `intprop.py` — 求解器：`Solver`、`floor_div`/`ceil_div`、独立检查器
  `check_assignment`、参考枚举器 `enumerate_solutions`
- `test_intprop.py` — unittest 测试套件（27 个测试）
- `demo.py` — 运行样例

## 设计要点

- **取整方向**：正系数收紧上界用 `floor_div`，负系数收紧下界用
  `ceil_div`，均为严格数学 floor/ceil 语义，任意符号正确。
- **大整数**：全部使用 Python 任意精度整数，无溢出（测试覆盖 ±2^260）。
- **等式拆分**：`==` 拆成 `<=` 与 `>=` 两个方向分别传播。
- **队列传播**：变量界变化时，监视该变量的所有约束入队，传播至固定点；
  每次收紧记录解释（`why_lo`/`why_hi`，`explain(var)` 查看）。
- **回滚**：`checkpoint()` / `undo(mark)` 基于 trail 恢复域、解释并移除
  后加的约束，冲突状态随之清除。
- **模同余必要条件**：`sum a_i x_i` 恒为 `g = gcd(a_i)` 的倍数。等式要求
  `g | rhs`；不等式把 rhs 收紧到 `g*floor(rhs/g)`。可发现纯区间传播漏掉
  的无解（如 `2x+2y+2z == 3`），但求解器**不完备**：域未收敛为单点时
  只报 `UNKNOWN`。
- **SAT 验证**：仅当所有变量收敛为单点时才声称 `SATISFIABLE`，且赋值
  必须通过独立检查器在原始约束（未拆分、含原 sense）上验证。
- **零系数**：入约束时丢弃，不影响任何域；**常量矛盾**（如 `0 <= -1`）
  立即失败。

## 运行

```sh
python3 demo.py          # 运行样例
python3 -m unittest -v   # 测试
```

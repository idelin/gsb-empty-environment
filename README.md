# 有限域排列约束求解器（allDifferent + 逆序数上下界）

纯 Python 3.11 标准库实现，不调用任何 SAT/SMT 库。

## 文件

- `perm_solver.py` — 求解器主体
  - `min_max_inversions(domains, values)`：bitmask DP 精确计算当前域下
    所有满足 allDifferent 的完备赋值的逆序数区间 `[lo, hi]`
    （必然界/可能界）；无完备赋值时返回 `None`。
  - `propagate(...)`：对每个 `变量=值` 计算固定后的可达区间，与
    `[low, high]` 不相交（或 allDifferent 不可满足）则剪枝，迭代至不动点；
    每次剪枝/失败都通过日志回调输出依据。
  - `PermSolver`：MRV 分支回溯搜索；进入分支前复制域，回溯即恢复域；
    每次剪枝、分支、回溯均记录到 `trace`。
  - `check_solution(...)`：独立检查器，直接验证排列性、原域成员性，
    并用独立的 O(n²) `inversion_count` 验证逆序数上下界。
  - `brute_force_solutions(...)`：小规模全排列穷举，用于对照。
- `test_perm_solver.py` — unittest 测试（19 个）。

## 关键性质

- 传播是精确的：DP 精确枚举所有完备赋值的逆序数范围，因此剪枝达到
  弧一致；测试用 80 个随机小规模实例与穷举解集投影逐变量比对，
  保证“仍有合法完成方案的值不会被删除”。
- 上下界矛盾（`low > high`）立即失败；可达区间与允许区间不相交立即失败。
- 固定排列 `[3,1,2]` 逆序数 = 2（逆序对 (3,1)、(3,2)）。

## 运行

```bash
python3 perm_solver.py          # 运行样例（含每次剪枝依据）
python3 -m unittest -v          # 运行全部测试
```

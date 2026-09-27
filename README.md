# Regular Constraint over a DFA

变量序列 `X_0 .. X_{n-1}` 必须被给定 DFA 接受。纯标准库，Python 3.11，
不调用任何 SAT/SMT 求解器。

## 文件

- `automaton.py` — 完备 DFA 的定义与独立的接受性判定 `accepts(word)`。
- `regular_constraint.py` — 弧一致传播器 `RegularConstraint`。
- `independent_checker.py` — 独立检查器：见证验证 + 穷举枚举真值。
- `examples.py` — 可运行样例（`python3.11 examples.py`）。
- `test_regular_constraint.py` — unittest（`python3.11 -m unittest -v`）。

## 过滤原理

对每个位置 `i` 维护两层状态集合：

- `fwd[i]`：读完前 `i` 个符号后可达的状态（`fwd[0] = {start}`）；
- `bwd[i]`：从该状态出发读后缀仍能到达接受态的"活"状态
  （`bwd[n] = accepting`）。

值 `v` 在位置 `i` 有支持，当且仅当存在 `q ∈ fwd[i]` 使
`δ(q, v) ∈ bwd[i+1]`。只能走死路径的值无支持，被 `filter()` 删除。

## 性质

- **空序列**：`n = 0` 时可解当且仅当起始态是接受态。
- **增量更新**：`remove_value(i, v)` / `set_domain(i, values)` 只重算
  受影响的层（`fwd[i+1..n]` 与 `bwd[i..0]`）。
- **独立验证**：声称有解时 `witness()` 给出一个词，
  `check_witness` 直接对照原始域与 `DFA.accepts` 验证；
  `exhaustive_support` 穷举所有序列给出过滤结果的对照真值。

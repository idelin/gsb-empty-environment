# 压缩元组表约束（Python 3.11，仅标准库）

## 设计

- **共享前缀压缩**：`SharedPrefixTable` 用 trie 存储元组，公共前缀只存一份；
  重复元组通过 id 去重，只存储、只计数一次。
- **支持维护**：`Engine` 持有有限变量域；`remove_value` 删值后，经
  `(位置, 值) -> 元组id` 索引同步失效所有相关元组（`bad` 计数 0→1 时
  正表元组死亡并撤销其全部支持，支持归零的值被级联剪枝）。禁表维护
  `smatch`（单例匹配数）：全部匹配即冲突，剩一个自由位置则剪掉该位置的值。
  所有变更记录 trail，`mark()/undo()` 支持回溯。
- **正表 / 禁表**：正表为空 → 不可行（`Infeasible`）；禁表为空 → 无限制。
- **无 SAT/SMT**：内置回溯求解器（最小剩余域启发式）。
- **独立检查器**：`check_solution` 只对照原始输入元组列表（`raw_tuples`），
  不接触 trie 等压缩结构；`solve` 声称有解时必先通过该检查。

## 文件

- `table_constraints.py` — 核心库（trie、约束、引擎、求解器、检查器）
- `demo.py` — 运行样例
- `test_table_constraints.py` — unittest 测试（18 个用例）

## 运行

```sh
python3.11 demo.py
python3.11 -m unittest test_table_constraints -v
```

# 有界编辑距离状态机（Python 3.11，仅标准库）

给定模式 `pattern`、显式字母表 `alphabet` 和预算 `k`，构建一个**状态与转移完全显式**的
编辑距离 NFA。支持流式输入，维护「状态 → 最优成本」的活状态集合；到达接受状态时返回
最小编辑距离与编辑路径（含对齐）。不使用任何正则或自动机第三方库。

## 模型

- 普通状态 `("n", i)`：已消费模式前 `i` 个字符。
- 待完成交换状态 `("t", i)`：读入了 `p[i+1]`（相邻交换的一半），还需读 `p[i]`。
- 起始状态 `("n", 0)`，唯一接受状态 `("n", len(pattern))`。
- 边（成本显式标注）：
  - 匹配/替换：`("n",i) --c--> ("n",i+1)`，成本 `0` 或 `substitution_cost`
  - 插入：`("n",i) --c--> ("n",i)`，成本 `insertion_cost`
  - 删除（ε 边）：`("n",i) -ε--> ("n",i+1)`，成本 `deletion_cost`
  - 相邻交换（仅当显式声明 `transposition_cost`）：
    `("n",i) --p[i+1]--> ("t",i) --p[i]--> ("n",i+2)`
- 成本超过 `k` 的状态立即剪枝；同状态由多条路径到达时只保留最小成本
  （`Stream._relax`，严格更小才替换）。
- 默认**不**把相邻交换算作单次编辑；`ba` 对 `ab` 默认距离为 2。
- 空模式只有一个状态，匹配长度（插入次数）`<= k` 的字符串。

## 文件

- `edit_machine.py`：`EditDistanceNFA`（机器）、`Stream`（流式状态集）、
  `EditDistanceNFA.build(...)`（按模式构造）以及便捷函数 `distance(...)`。
  也可直接用 `EditDistanceNFA(states, start, accepts, symbol_transitions,
  epsilon_transitions, alphabet, k)` 传入**显式转移结构**。
- `demo.py`：运行样例（逐符号打印活状态集合）。
- `counterexample.py`：可独立执行的反例脚本。
- `test_edit_machine.py`：11 个 `unittest` 用例（含与经典 DP 编辑距离的交叉验证）。

## 运行

```sh
python3.11 demo.py
python3.11 counterexample.py
python3.11 -m unittest test_edit_machine -v
```

## 样例输出（节选）

```
== classic Levenshtein: substitute + insert
pattern='kitten' text='sitting' k=2
 after 6 symbol(s): {('n', 6): 2}
 after 7 symbol(s): {}
no match (distance > k)          # sitting 距离为 3，k=2 时被正确拒绝

== deletion on the stream, explicit costs
pattern='cat' text='ct' k=2 kwargs={'deletion_cost': 2}
distance=2
  text:    c-t
  pattern: cat
  path:    0:match 2:delete(a) 0:match

== empty pattern accepts strings of length <= k
pattern='' text='abc' k=3
distance=3
  path: 1:insert(a) 1:insert(b) 1:insert(c)

== adjacent swap is NOT one edit by default
pattern='ab' text='ba' k=1
no match (distance > k)          # 默认距离 2

== adjacent swap allowed when explicitly declared
pattern='ab' text='ba' k=1 kwargs={'transposition_cost': 1}
distance=1
  text:    ba
  pattern: ab
  path:    1:transpose(ab->ba)
```

## 反例

`python3.11 counterexample.py`：

```
[PASS] swap costs 2 by default: 'ba' vs 'ab' accepted=False (distance 2 > k=1)
[PASS] swap costs 1 only if declared: 'ba' vs 'ab' accepted=True distance=1
[PASS] empty pattern: len <= k: 'abc' accepted=True, 'abcd' accepted=False
all counterexamples hold
```

## 测试结果

`python3.11 -m unittest test_edit_machine -v`：11 个用例全部 `ok`，`Ran 11 tests ... OK`。
覆盖：经典 Levenshtein 路径、与 DP 交叉验证、空模式长度界、交换默认关闭/声明开启、
显式成本、流式同状态最优成本合并、增量与一次性输入一致、路径重建往返、
手写显式状态机构造、字母表越界检查。

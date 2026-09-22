# Edit Distance Automaton (Python 3.11, stdlib only)

为给定模式构建编辑距离 ≤ k 的状态机，插/删/改成本显式，不使用任何现成正则或自动机库。

## 文件

- `edit_distance_automaton.py` — 核心模块：`EditCosts`、`EditOp`、`EditDistanceAutomaton`
- `test_edit_distance_automaton.py` — unittest 测试（19 个用例，可独立执行）
- `demo.py` — 运行样例

## 设计要点

- **显式字母表与转移结构**：构造时传入 `alphabet`；`transitions(pos, ch)`（消耗输入）
  与 `epsilon_transitions(pos)`（删除，不消耗输入）显式返回 `(下一状态, 成本, 操作)` 列表。
- **流式处理**：`step(ch)` 逐符号推进，状态集合为 `{模式位置: 最优成本}`；
  不同路径到达同一状态时只保留最小成本，超过 k 的状态被剪枝。
- **编辑路径**：`min_distance_and_path(text)` 用全量 DP + 回溯返回
  `(最小距离, [EditOp...])`，距离为全局最小值（可超过 k）。
- **空模式**：恰好匹配长度 ≤ k 的串（成本 = 长度 × 插入成本）。
- **相邻交换**：默认不算单次编辑（等于删+插或两次替换）；
  仅当 `EditCosts(transpose=...)` 显式声明时才作为单步操作。

## 运行

```sh
python3.11 demo.py                 # 运行样例
python3.11 -m unittest -v          # 测试（OK, 19 tests）
```

## 示例

```python
from edit_distance_automaton import EditCosts, EditDistanceAutomaton

aut = EditDistanceAutomaton("kitten", "aeikgnst", k=3)
accepted, dist, ops = aut.explain("sitting")
# accepted=True, dist=3
# ops: sub(k->s)+1 match(i) match(t) match(t) sub(e->i)+1 match(n) ins(g)+1

# 流式
aut.reset()
for ch in "sitting":
    aut.step(ch)
    print(aut.states)      # {模式位置: 最优成本}
aut.accepts()              # True
```

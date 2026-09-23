# productpath — 顶点×状态乘积空间求路

Python 3.11，仅标准库，测试用 unittest。

## 模型

- **图** (`productpath/graph.py`)：有向多重图。顶点、边均为字符串 ID；孤立点与多重边保留；边携带非负长度与状态变换 `state_in -> state_out`（当前状态等于 `state_in` 才可通行，通过后变为 `state_out`）。
- **禁行规则** (`productpath/rules.py`)：`ForbiddenRule(vertex, state, start, end)` 禁止在到达时刻 `t ∈ [start, end)`（左闭右开，`None` 表示无界）以 `state` 状态经过 `vertex`。`RuleSet` 每次增删规则版本号 +1。
- **搜索** (`productpath/search.py`)：在 `(顶点, 状态)` 乘积空间做最佳优先搜索。只考虑不重复乘积状态的路径，因此零权环不会使搜索无限进行；同一顶点不同状态互不剪枝。路径按三级键排序：**成本 → 边数 → 边ID 序列**，零权环下仍有最小解。输出包含状态轨迹的 `Path`。
- **前K条枚举**：直接在禁行约束下枚举（不是先找无约束前K再过滤）。`Cursor` 绑定规则版本，跨页续枚举；规则更新后旧游标抛出 `StaleCursorError`。
- **预算**：`budget` 限制候选扩展量；触限时 `SearchResult.pending` / `pending_states` 给出预算未决状态，`expansions` 记录候选扩展量，`exhausted` 标记搜索空间是否穷尽。
- **独立穷举校验** (`productpath/bruteforce.py`)：`brute_force_paths` 用 DFS 穷举小乘积图的全部合法简单路径并排序，用于完整性对拍（见 fuzz 测试）。

## API 速览

```python
from productpath import Graph, RuleSet, ForbiddenRule, find_paths, shortest_path

g = Graph()
g.add_edge("e1", "s", "m", 1.0, "walk", "ride")
g.add_edge("e2", "m", "t", 2.0, "ride", "walk")

rules = RuleSet()
rules.add_rule(ForbiddenRule("m", "ride", start=1.0, end=3.0))  # t∈[1,3) 禁行

res = shortest_path(g, rules, "s", "walk", "t")
res.paths[0].states   # 状态轨迹
page1 = find_paths(g, rules, "s", "walk", "t", k=2)
page2 = find_paths(g, rules, "s", "walk", "t", k=2, cursor=page1.cursor)
```

## 运行

```bash
python3 examples/demo.py                       # 运行样例
python3 -m unittest discover -s tests -t . -v  # 测试
```

## 运行样例输出（节选）

```
== 4. 新增禁行规则(版本=1): 地铁站@乘车 t∈[1,3) 禁行, 较短非法路径让位给较长合法路径 ==
  #1 cost=3.5 hops=3 edges=('e5', 'e6', 'e7') vertices=('家', '公园', '家', '公司') states=('步行', '步行', '休息过', '步行')
== 7. 规则更新到版本=2 后旧游标被拒绝 ==
  cursor is bound to rule version 1, current version is 2
== 8. 预算=2 的受限搜索(记录候选扩展量与预算未决状态) ==
  候选扩展量=2 搜索完备=False
  预算未决状态=(('公司', '步行'),)
== 9. 与独立穷举校验一致性: OK (共2条) ==
```

## 测试结果

```
Ran 20 tests in 0.003s
OK
```

覆盖：三级决胜、状态轨迹、禁行让位、左闭右开区间、版本更新与游标失效、
前K直接枚举（非过滤）、预算未决状态、零权环终止、同顶点异状态不互剪、
孤立点/多重边/字符串ID，以及 60 组随机小乘积图对拍独立穷举。

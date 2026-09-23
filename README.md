# product_paths — 顶点 × 状态乘积空间的前 K 条合法路径枚举

Python 3.11，仅使用标准库；测试基于 `unittest`。

## 模型

- **图**（`product_paths/graph.py`）：顶点与边均使用字符串 ID；保留孤立点与
  多重边（平行边以不同边 ID 区分）。边携带非负长度（零权允许，负权拒绝）和
  状态变换表 `transitions: {输入状态: 输出状态}`。
- **禁行规则**（`product_paths/rules.py`）：规则禁止进入某个乘积状态
  `(vertex, state)`，生效区间为**左闭右开** `[valid_from, valid_to)`
  （`valid_to=None` 表示无限期）。每次增删规则都会递增版本号并保留快照，
  查询可指定历史版本。
- **路径排序**：三级关键字 `(成本, 边数, 边ID序列)`。只考虑**不重复乘积状态**
  的路径，因此零权环下路径集合有限、最小解必然存在，搜索不会无限进行。

## 枚举算法

`enumerate_paths` 在乘积空间上做最佳优先枚举（优先队列按三级关键字弹出），
禁行规则在**扩展时**生效——不是先求无约束前 K 条再过滤。因此：

- 较短路径进入禁行状态时被丢弃，较长的合法路径自然递补；
- 同一顶点的不同状态互为独立乘积状态，不会互相剪枝；
- 每条路径维护已访问乘积状态集合，零权环只会产生有限条简单路径。

游标分页：`SearchResult.cursor` 绑定签发时的规则版本；规则更新后用旧游标
继续查询会抛出 `CursorVersionError`，显式指定旧版本号则可一致地继续翻页。

预算与统计：`max_expansions` 限制候选扩展量；`SearchStats` 记录
`expansions`（候选扩展量）、`generated`、剪枝计数，以及预算耗尽时仍未扩展的
 frontier 末端状态 `pending_states`（预算未决状态）。

## 独立校验

`product_paths/verify.py` 中的 `brute_force_paths` 用显式 DFS 栈独立穷举全部
合法乘积简单路径并排序；`verify_enumeration` 将最佳优先枚举结果与之比对，
返回 `VerificationReport`（含双方的扩展量与预算未决状态）。随机小图回归见
`tests/test_verify.py`。

## 运行

```bash
python3.11 examples/demo.py          # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 运行样例输出（节选）

```text
== Top-5 legal paths A -> D, initial state 'clear' (no rules) ==
  1. cost=1 edges=3 ids=('e2', 'e7', 'e5')
     trajectory: A[clear] -> B[clear] -> C[clear] -> D[clear]
  2. cost=2 edges=2 ids=('e1', 'e3')
     trajectory: A[clear] -> B[loaded] -> D[clear]
  ...
== Top-3 at t=15 (rule [10, 20) active: (C, clear) forbidden) ==
  1. cost=2 edges=2 ids=('e1', 'e3')      # 更短的 cost=1 路径进入禁行状态，让位于较长合法路径
  2. cost=2 edges=2 ids=('e2', 'e3')
== Cursor pagination (page size 2, version 2) ==
  page 1: [('e2', 'e7', 'e5'), ('e1', 'e3')]
  page 2: [('e2', 'e3'), ('e4', 'e5')]
  page 3: [('e4', 'e6', 'e3')]
  stale cursor rejected: cursor was issued for ruleset version 2, ...
== Independent brute-force verification ==
  matches=True compared=5
```

## 测试结果

```text
Ran 39 tests in 0.059s
OK
```

覆盖：图模型（孤立点/多重边/负权拒绝）、规则版本与左闭右开区间、三级排序
决胜、零权环终止性、同顶点异状态不互剪、禁行让路、版本化查询、游标分页与
版本绑定、预算未决状态记录，以及 200+60 个随机小乘积图的穷举对拍。

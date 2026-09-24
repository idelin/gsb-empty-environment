# graphmatch — 增量小型模式图匹配引擎

Python 3.11，仅标准库，测试使用 unittest。

## 功能

- **模式图**：顶点带类型与属性谓词（`AttrPred`，支持 eq/ne/lt/le/gt/ge/exists）；
  边支持存在（positive）与不存在（negative，`exists=False`）约束，边同样带类型与属性谓词。
- **匹配语义**：顶点映射单射（两个模式点不得映射到同一目标点）；平行目标边
  （多重边）不会重复产生相同顶点映射；孤立点保留并可参与匹配；顶点、边均为字符串 ID。
- **时间区间**：边有效期为左闭右开 `[valid_from, valid_to)`，匹配在指定时刻 `time` 求值。
- **稳定输出**：结果按稳定映射 ID（`p1=v1;p2=v2`，按模式点排序）字典序输出；
  去同构策略可选 `DedupStrategy.NONE` / `DedupStrategy.AUTOMORPHISM`
  （按模式自同构群取商，输出等价类最小代表 ID）。
- **增量维护**：`apply_update` 批量增删边，返回稳定映射 ID 的增删差量 `Delta`。
  负边约束下新增边也会撤销结果。更新通过「匹配索引 + 锚点枚举」完成，
  绝不重扫全部目标映射：
  - 撤销类（删正边、加负边）：经索引 `(模式边, 目标源, 目标宿) -> 匹配ID` 直接定位受影响匹配；
  - 新增类（加正边、删负边）：仅以变更边端点为锚点枚举扩展映射。
- **候选限制**：`max_candidates` 限制每次操作触及的候选数量，超限置 `truncated` 标志。
- **枚举恢复**：`enumerate(limit, cursor)` 分页枚举；游标绑定图版本，
  中断后可在同一图版本上恢复，版本变化抛 `VersionMismatchError`。
- **统计**：`last_op` / `stats` 记录候选集合触及量与完整有效性检查次数。

## 运行

```bash
python3 examples/demo.py     # 运行样例
python3 -m unittest discover -s tests -v   # 测试
```

## 运行样例输出

```
== initial matches (paged enumeration, 2 per page) ==
    {'a': 'ann', 'b': 'bob', 'c': 'cid'}
    {'a': 'bob', 'b': 'cid', 'c': 'dan'}
 -- interrupted, saved cursor: 0|a=bob;b=cid;c=dan
    {'a': 'cid', 'b': 'dan', 'c': 'eve'}
    {'a': 'dan', 'b': 'eve', 'c': 'ann'}
    {'a': 'eve', 'b': 'ann', 'c': 'bob'}
 -- resumed to completion on the same graph version

== batch update 1: ann meets cid (k6), bob forgets cid (k2) ==
version=1
 added: ['a=ann;b=cid;c=dan', 'a=eve;b=ann;c=cid']
 removed: ['a=ann;b=bob;c=cid', 'a=bob;b=cid;c=dan']
 candidates touched: 12

== batch update 2: cid meets ann (k7) ==
version=2
 added: ['a=cid;b=ann;c=bob']
 removed: []
 candidates touched: 6
```

## 测试结果

`python3 -m unittest discover -s tests`：**16 个测试全部通过（OK）**，覆盖：

- 类型 + 属性谓词 + 单射约束；孤立点保留；多重边去重；左闭右开时间区间；
- 三点负边模式：目标多出指定边则排除，增删负边引起结果撤销/恢复；
- 去同构策略（对称模式 2 映射 → 1 等价类）与稳定顺序/稳定 ID；
- 分页枚举中断恢复、版本失配抛错、候选数量限制截断；
- 随机批量更新序列（含对称模式、多重边、负边）每步与全量穷举
  （`brute_force_match_ids`）对照匹配集合与增删差量，并断言增量候选
  触及量显著低于每步全量重算（禁止重扫全部目标映射）。

## 结构

- `graphmatch/graph.py` — 目标图（字符串 ID、多重边、孤立点、时间区间）
- `graphmatch/pattern.py` — 模式图（类型、属性谓词、正/负边约束）
- `graphmatch/matcher.py` — 增量匹配器（索引 + 锚点枚举、差量、游标、统计）
- `graphmatch/bruteforce.py` — 独立全量穷举参照实现（测试对照用）
- `tests/test_graphmatch.py` — unittest 测试套件
- `examples/demo.py` — 运行样例

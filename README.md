# pattern-matcher

小型模式图匹配引擎（Python 3.11，仅标准库，unittest 测试）。

## 功能

- **模式图**：顶点带类型与属性谓词（`eq/ne/lt/le/gt/ge/in/exists/missing`），
  边分**存在**（正边）与**不存在**（负边）约束，均可带类型与属性谓词。
- **目标图**：顶点、边均为字符串 ID；保留孤立点与多重边（平行边）；
  边可带有效期区间 `[valid_from, valid_to)`（**左闭右开**），匹配按 `at_time` 过滤。
- **匹配语义**：顶点映射**单射**（两个模式点不能映射到同一目标点）；
  正边要求至少一条见证边，负边要求不存在见证边；
  平行目标边在 `VERTEX_MAPPING` 策略下不重复产生同一顶点映射。
- **输出**：稳定排序（按模式点 ID 序取目标点 ID 元组）；映射 ID 由映射内容
  SHA1 派生，跨更新稳定。
- **去同构策略**（`DedupStrategy`）：
  - `VERTEX_MAPPING`：每个顶点映射一条结果；
  - `EDGE_EMBEDDING`：按见证边组合区分不同嵌入。
- **限制**：`max_candidates`（每个模式点的候选数上限）、`max_matches`。
- **增量维护**（`IncrementalMatchEngine`）：目标图支持**批量增删边**；
  每次更新输出带稳定映射 ID 的**增删差量**（`Diff.added/removed`）。
  负边约束使**新增边也可能撤销**已有结果，删除边也可能产生新结果。
  增量算法不重扫全图：
  - 撤销：仅重验映射像触及变更边端点的已有匹配；
  - 发现：由变更边生成种子映射（新增边→正边端点；删除边→负边端点），
    仅在变更邻域内补全。
- **度量**：`Metrics.candidates_touched`（候选集合触及量）与
  `matches_revalidated` 记录每次更新工作量。
- **断点恢复**（`MatchEnumerator`）：枚举可分页中断，凭 `cursor` 在
  **同一图版本**上恢复；图版本变化后继续使用抛 `StaleVersionError`。

## 结构

```
pattern_matcher/
  graphs.py    # 目标图/模式图数据模型、属性谓词、时间区间
  matcher.py   # 单射回溯枚举、去重策略、候选限制、可恢复枚举器、度量
  engine.py    # 增量维护引擎：批量更新 -> 增删差量
tests/         # unittest：含随机更新序列 vs 全量穷举的对照测试
examples/demo.py
```

## 运行

```bash
python3.11 -m examples.demo          # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 样例输出（`python3.11 -m examples.demo`）

```
== initial matches (t=150) (5) ==
  853e0d323b08bcd6  {'p': 'ann', 'q': 'bob', 'r': 'cid'}
  12bb1a8f22be6bd2  {'p': 'ann', 'q': 'bob', 'r': 'dan'}
  cee83e96ea514d3e  {'p': 'ann', 'q': 'cid', 'r': 'bob'}
  03bc6ef9c0727834  {'p': 'ann', 'q': 'dan', 'r': 'bob'}
  5681c3bc44ae6da3  {'p': 'ann', 'q': 'dan', 'r': 'cid'}
  initial candidates_touched=20

== batch update -> diff (version 1) ==
  + 7be6bf668800bb15  {'p': 'dan', 'q': 'bob', 'r': 'ann'}
  - 12bb1a8f22be6bd2  {'p': 'ann', 'q': 'bob', 'r': 'dan'}
  - 03bc6ef9c0727834  {'p': 'ann', 'q': 'dan', 'r': 'bob'}
  - 5681c3bc44ae6da3  {'p': 'ann', 'q': 'dan', 'r': 'cid'}
  update metrics: candidates_touched=13, matches_revalidated=5

== matches after update (3) ==
  853e0d323b08bcd6  {'p': 'ann', 'q': 'bob', 'r': 'cid'}
  cee83e96ea514d3e  {'p': 'ann', 'q': 'cid', 'r': 'bob'}
  7be6bf668800bb15  {'p': 'dan', 'q': 'bob', 'r': 'ann'}

incremental state == full re-enumeration: OK

== paged enumeration with resume ==
  page1: ['853e0d323b08bcd6']  cursor=(('ann', 'bob', 'cid'), 0)
  resumed rest: ['cee83e96ea514d3e', '7be6bf668800bb15']

== dedup strategies on parallel edges ==
  vertex_mapping: 1 match(es); edge_embedding: 2 match(es)
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests
.........................
----------------------------------------------------------------------
Ran 25 tests in 0.136s

OK
```

其中 `TestExhaustiveRandomComparison` 对随机图/随机模式（含对称模式、
多重边、负边）执行 40 步随机批量增删边，每步断言增量引擎结果与全量
穷举完全一致、差量可正确拼接，且增量更新的候选触及量不超过（局部更新
时严格小于）全量重扫。

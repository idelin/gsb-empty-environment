# minidb — 复合索引访问路径编译器 + MVCC 执行引擎

纯 Python 3.11 标准库实现，不调用任何 SQL 引擎。给定一张带稳定 row ID 的
合成表和若干复合索引（多列字典序键，每列独立 ASC/DESC 与 NULL 位置），把
类型化谓词树（比较 / IN / IS NULL / AND / OR / NOT）编译成可执行的访问
路径，并用一个真正的迭代器执行它。

## 模块

- `minidb/domains.py` — 键编码：每列值编码为可比较分量（DESC 用 `Rev`
  反转序、NULL 按 nulls_first/last 落位、±∞ 哨兵），索引键即分量元组。
- `minidb/predicates.py` — 谓词树与 Kleene 三值求值（True/False/UNKNOWN）；
  `negate()` 把 NOT 下推为否定范式（NNF）。
- `minidb/intervals.py` — 复合键区间代数：交、并（规范化合并）、补、凸包、
  按额度收缩（合并相邻区间，只产生超集，绝不丢分支）。
- `minidb/storage.py` — MVCC 表与索引：行和索引项都带 `[begin, end)` 版本
  区间；快照钉住读版本；`gc()` 回收所有快照都不可见的旧版本。
- `minidb/compiler.py` — 访问路径编译器与成本模型。
- `minidb/executor.py` — 执行游标：区间扫描 / 索引合并 / 全表扫描，
  分页与续读 token。
- `minidb/interpreter.py` — 独立的全表扫描参照实现（验收对照用）。

## 编译语义

对每个候选索引，谓词被编译成一组**互不重叠**的扫描区间（该索引编码键
空间内），外加**残余谓词**（恒为原始谓词，逐行复核）。核心不变式：

> 区间集合必须覆盖所有使谓词**非 FALSE**（TRUE 或 UNKNOWN）的行。

- 比较/IN 的区间自动带上 NULL 点（NULL 行使谓词 UNKNOWN，必须覆盖）；
  前缀列上的 `!=` / NOT IN / IS NOT NULL 用精确补集，后缀列上则退化为
  全宽区间——**补集不能作用于已加宽的多列凸包**，否则会静默丢行。
- NOT 先经 NNF 下推到原子再编译，保证三值逻辑正确（NOT UNKNOWN =
  UNKNOWN，该行被过滤）。
- 只约束后缀列的谓词产生全宽区间 + 残余谓词，绝不会被当成窄前缀区间。
- 区间数量超过 `interval_budget` 时，相邻区间按边界相似度合并为超集，
  置 `degraded=True`，残余谓词兜底，分支绝不截断。
- 顶层 OR 额外生成索引合并（union）候选：每个分支选最便宜的索引，
  执行期按 rowid 去重——同一 row ID 只读取/输出一次，但值相同的两条
  物理行仍是两条结果。

## 成本模型

`CostModel(page_cost, fetch_cost, compare_cost, page_size, interval_budget)`。
估算 = 页读数 × 页单价 + 回表行数 × 回表单价 + 比较次数 × 比较单价；
区间覆盖的索引项数在编译版本上精确计数，因此估算完全可复现。
`compile_query()` 返回 `(plan, report)`，report 列出全部候选的成本明细
与最终选择（成本最低，平局按 union < range < full 再按名字，确定性）。
执行侧 `cursor.stats` 记录实际发生的 `index_entries / row_fetches /
compares / pages`，可与估算对照。

## MVCC 与分页 token

每次增删改提交为新版本。游标整个生命周期固定在快照版本上读索引和堆，
键迁移（更新索引列）后旧游标仍读到旧键对应的行。token 为 JSON，绑定
`(计划+参数指纹, 快照 id, 位置, 累计统计)`：

```python
tok = cur.token()
cur = Cursor.resume(db, tok, plan, params)   # 计划/参数不符 -> PlanMismatch
                                             # 快照已释放   -> SnapshotReclaimed
```

## 运行

```bash
python3 -m unittest discover -s tests   # 单元 + 验收测试
python3 examples/demo.py                # 端到端示例（含成本报告）
```

验收测试在小域（含 NULL）上穷举 222 个谓词 ×（2 种区间额度 + 强制全表）
共 888 次计划执行，每次翻页之间插入增/删/键迁移、每个页边界都经过
token 序列化/续读，结果与独立全扫解释器在钉住版本上逐行比对。

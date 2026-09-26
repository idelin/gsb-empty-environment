# minisearch

本地合成 UTF-8 文本上的布尔 / 短语 / 模糊查询引擎，带**执行前成本估算**与
**执行中共享操作预算**。纯 Python 标准库（3.11+），不使用任何搜索服务或
全文检索库。测试使用 `unittest`。

## 分词规则

权威定义见 `minisearch/tokenizer.py` 模块 docstring：

1. 输入为 UTF-8 解码后的 `str`，先做 **NFKC 规范化**，再 **casefold**。
2. **CJK 表意文字**（U+4E00–9FFF、U+3400–4DBF、U+F900–FAFF）**逐字成词**
   （unigram），因为中文等文本无空白分词。
3. 其余 Unicode 字母/数字（`str.isalnum()`）的极大连续段构成一个词。
4. 空白、标点、符号一律为分隔符，不产生词。
5. 位置从 0 递增，供短语查询做相邻性检查。

## 查询类型

`Term` / `Phrase` / `Fuzzy(term, max_edits)` / `And` / `Or` / `Not`，
可任意嵌套。打分：Term/Fuzzy 用词频，Phrase 用匹配次数，Not 固定 1.0，
And/Or 为子结果分数之和。

## 成本模型（估算）

- `Term`：posting 条目数
- `Phrase`：各词 posting 之和 + 最小 posting × 4（位置对齐启发值）
- `Fuzzy`：词表大小 × (len+1)²（编辑距离 DP 上界）+ 命中词 posting 估计
- `And`/`Or`：子估算之和 × 2（求值 + 合并）
- `Not`：子估算 + **文档总数**（补集必须扫全表，宽泛 NOT 因此昂贵）

估算是启发式的，只用于执行前快速拒绝；执行中按**真实操作**逐次计费
（posting 扫描、集合合并、位置检查、DP 单元格），实际超支同样中断。

## 预算语义

- 每次 `search` 创建**一个** `Budget`，传入整棵查询树；子查询只 charge，
  **不能各自重置**（`tests/test_budget.py::test_shared_budget_across_subqueries`
  专门验证：两个子查询各自够、合计超支时必须中断）。
- 估算 > 预算：执行前拒绝，`incomplete=True`，`ops_used=0`。
- 执行中耗尽：抛 `BudgetExhausted` 并被捕获，`incomplete=True`。
- **超限时 `total_hits=None`**：绝不把截断的部分结果伪报为总命中；
  `stats.reason` 说明原因，`stats.ops_used` 给出实际消耗。
- **宽泛 NOT 受控**：补集逐文档计费 + 估算含文档总数，小预算直接被拒。

## limit 与预算耗尽的区别

| | `incomplete` | `total_hits` | `stats.limit_applied` |
|---|---|---|---|
| 正常 limit 截取 | `False` | 精确总数 | `True` |
| 预算耗尽/拒绝 | `True` | `None`（未知） | `False` |

## 运行

```bash
python3 demo.py                       # 运行样例
python3 -m unittest discover -s tests # 27 个测试
```

## 文件

- `minisearch/tokenizer.py` — 分词规则
- `minisearch/index.py` — 内存倒排索引
- `minisearch/queries.py` — 查询节点与成本估算
- `minisearch/budget.py` — 共享预算与 `BudgetExhausted`
- `minisearch/engine.py` — 执行器、结果与统计
- `tests/` — unittest 测试（分词、查询正确性、预算、limit 区分）
- `demo.py` — 运行样例

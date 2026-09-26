# 多字段阈值式 TopK 融合（Python 3.11，仅标准库）

本地合成 UTF-8 文本上的字段级检索 + 阈值式 TopK 融合教学实现。
**不使用任何搜索服务、数据库或全文检索库**，仅依赖 Python 3.11 标准库。

## 运行

```bash
python3.11 run_sample.py            # 默认查询 "检索 融合"
python3.11 run_sample.py "fusion"   # 自定义查询
python3.11 -m unittest discover -s tests -v
```

## 数据与分词

- 语料：`data/corpus.tsv`，UTF-8，每行 `doc_id<TAB>field<TAB>text`；
  某文档没有某字段行即该字段**缺失**（区别于空文本）。
- 分词规则（`fusion/tokenizer.py`，完全确定、可复算）：
  1. NFKC 规范化 + `casefold`（全角/半角、大小写归一，如 `ＡＢＣ→abc`）；
  2. 连续拉丁字母/数字切成一个词元（`Full-Text2→[full,text2]`），下划线与
     标点只作分隔符；
  3. 每个 CJK 统一表意文字单独成词（`全文→[全,文]`）；
  4. 其余符号（emoji、空白等）不产出词元；不做停用词与词干化。

## 字段分与归一化

- 原始字段分 = 查询词（去重）在该字段文本中的词频之和；只保留命中文档。
- 每个 (查询, 字段) 生成**不可变固定快照** `Snapshot`：posting 按
  `(-raw, doc_id)` 确定序排列，并带**明确统计** `min_raw/max_raw`。
- 归一化只用快照统计：`n = (raw - min) / (max - min)`。
  极差为 0（全同分或单候选）时公开约定全部记为 `1.0`，避免除零，也避免把
  并列候选错误打成 0。归一化基准只由固定快照决定，分页只是视图，
  **更换候选页/候选集不改变归一化基准**。

## 缺失字段与融合分

文档 `d` 只在其**实际命中的正权字段**集合 `P(d)` 上做权重平均：

```
score(d) = Σ_{f∈P(d)} w_f·n_f(d) / Σ_{f∈P(d)} w_f
```

- 缺失字段**不按 0 惩罚**（不进分子也不进分母），`Hit.missing_fields` 明示；
- 在所有正权字段都未命中的文档不参与排名；
- 权重 0 字段不取候选、不进分母、不影响排名。

## 阈值式 TopK 与停止证明

`fusion/fusion.py::fuse_topk` 为每个字段维护一个快照分页器，
`cap()` = 游标头 posting 的归一化分（降序，故为剩余候选的字段分上界），
每次只对 `cap` 最大的未耗尽字段取下一页（按需取候选）。

对部分命中文档，未取字段乐观取 `cap`；由于分母随出现集合变化，
`greedy_upper_bound` 用"按 cap 降序、高于当前平均才加入"的单遍贪心
（分数规划/分数背包性质，代码注释含正确性论证）求最优可达平均分；
未见过的文档取 `max(cap)`。

文档在每个字段"已命中或该字段列表已耗尽"时标记**已定**（exact）。
当已定文档不少于 K，且第 K 名的排序键 `(score, -id)` **严格大于**
每个未定候选与未见候选的上界键时停止——严格大于保证并列不会被提前截断；
否则继续取下一页，直到上界证明成立或全部字段耗尽。该条件即剩余得分上界
给出的充分证明，因此阈值结果与全量参考 `exhaustive_fuse` 逐位一致
（测试对 6 个查询及多种页大小做了核验）。

## 拒绝 / 退化行为（全量融合参考核验）

- 负权重：`NegativeWeightError`（负权破坏单调上界，直接拒绝）；
- 权重全 0 / NaN / Inf：`ValueError` 拒绝；
- 字段无界（`bounded=False`，上界假设不满足）或运行中观测到归一化分越界
  （违反 `n∈[0,1]`）：**拒绝提前停止，退化为全量精确融合**
  （`mode=degraded_exhaustive`，`degrade_reasons` 记录原因），结果仍精确；
- 固定快照 + 游标分页保证全量/退化读取**不重不漏**（有专门断言与测试）。

## 输出（分项得分）

`FuseResult` 含：`hits`（每个 `Hit` 有总分 `score`、各字段归一化分
`field_scores`、缺失字段、是否已定）、`mode`、是否退化及原因、停止原因、
每字段取页数 `pages_fetched`、见过文档数与完整检索轨迹 `trace`。

## 代码结构

```
fusion/tokenizer.py   分词规则
fusion/corpus.py      UTF-8 TSV 语料加载
fusion/index.py       词频打分、固定快照、分页器与 cap 上界
fusion/normalize.py   min/max 归一化与全同分除零防护
fusion/fusion.py      权重校验、缺失规则、贪心上界、阈值停止、拒绝/退化、全量参考
run_sample.py         运行样例
tests/                unittest 套件（37 个用例）
data/corpus.tsv       合成 UTF-8 文档集
```

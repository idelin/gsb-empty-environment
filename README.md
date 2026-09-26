# 多字段阈值式 TopK 融合

纯 Python 3.11 标准库实现（仅 `unittest` 用于测试）。文档为本地合成 UTF-8
文本，**不使用任何搜索服务或全文检索库**。各字段独立检索、按给定权重融合，
输出分项得分；TopK 采用 Fagin 风格的阈值算法（TA），每个字段只按需拉取候选，
停止条件由剩余得分上界严格证明。

## 目录结构

```
fusion/
  tokenizer.py   # 分词器（规则见下）
  normalize.py   # 基于明确统计的 min-max 归一化
  index.py       # 文档、倒排索引、按查询的字段访问视图
  fusion.py      # 权重校验、聚合、阈值 TopK、全量参考融合
  engine.py      # 引擎：拒绝/退化、参考核验、固定快照分页
  demo.py        # 运行样例：python3.11 -m fusion.demo
tests/           # unittest 测试（43 个用例）
```

## 分词规则（固定、公开）

文档与查询使用**同一个**分词器：

1. 输入为已按 UTF-8 解码的 `str`。
2. ASCII 字母数字（`A-Za-z0-9`）的最长连续序列为一个 token，统一小写；
   `_` 等其余 ASCII 符号是分隔符。
3. 每个 CJK 统一表意文字（U+4E00–U+9FFF）单独成为一个单字 token。
4. 其余所有字符（空白、标点、其他文字如拉丁扩展字符）均为分隔符，不产生 token。

例：`"Hello, 世界! ML_2024"` → `["hello", "世", "界", "ml", "2024"]`

## 字段打分与归一化（只使用明确统计）

- 字段原始分：`raw(d, f) = Σ_{t ∈ 查询词集合} tf(t, d.f)`（词频求和）。
- 归一化采用 min-max，**只使用明确统计**：`FieldStats(min, max)` 在查询开始
  时基于**整个索引中所有拥有该字段的文档**计算一次并冻结，随后写入结果
  `meta["stats"]`。它绝不从"当前已取候选集"重新估计——因此更换候选集（提前
  停止、分页、不同 k）不会无说明地改变归一化基准。
- 更换语料/索引是**显式**的基准变更：`Engine.update_index()` 递增
  `index_version`，新查询使用新统计；旧快照继续提供其冻结数据，互不影响。
- **全同分字段避免除零**：`max == min` 时，共同值非零则归一化为 `1.0`，
  全为零则为 `0.0`（该字段无区分度，但保持良定义）。结果裁剪到 `[0, 1]`。

## 缺失字段规则（不以 0 错误惩罚）

- 文档**拥有**字段但无查询命中：真实原始分 0，正常参与归一化与融合。
- 文档**缺失**字段：该字段从分子与分母中同时剔除，绝不按 0 惩罚：

```
score(d) = Σ_{i ∈ d拥有的激活字段} w_i · n_i(d)  /  Σ_{i ∈ d拥有的激活字段} w_i
```

- 该规则不破坏上界：得分是不超过 `max_i t_i` 的若干值的加权平均（见下）。

## 阈值式 TopK（TA）与停止条件证明

- 每个字段一条 sorted-access 流（原始分降序、doc_id 升序打破平局），**按需
  拉取**；见到文档即对其余字段做 random access 算出精确融合分。
- 设 `t_i` 为字段 `i` 当前归一化阈值（最近一次 sorted-access 拉取值的归一化
  结果，初始为统计最大值）。sorted-access 非增 + 归一化单调 ⇒ 任何未完全确定
  的文档 `d` 满足 `n_i(d) ≤ t_i`。权重全部为正 ⇒ `score(d)` 是若干均不超过
  `max_i t_i` 的值的加权平均（缺失字段只是让平均跑在子集上），故

```
任何未见文档:  score(d) ≤ B := max_i t_i
```

- **停止条件**：当已精确评分的第 k 名 **严格大于** `B` 时停止。严格大于保证
  `(-score, doc_id)` 的确定性平局规则不会被未见文档颠覆。阈值单调下降且流有
  限 ⇒ 算法必然终止（最坏退化为全量）。流耗尽后对拥有激活字段的剩余文档精确
  评分，结果与全量参考完全一致。
- 元数据公开停止证据：`meta` 含 `stopped_early`、`bound`、`kth_score`、各字段
  `pulls`（拉取次数，可验证"只按需取候选"）。

## 负权与假设违反：拒绝 / 退化

- **负权重使上界失效**。`Engine(on_negative_weights="reject")`（默认）抛出
  `NegativeWeightError`；`"degrade"` 模式退化为全量参考融合（带符号权重，
  拥有字段权重和 ≤ 0 的文档被丢弃），并在 `meta` 中标记
  `degraded=True` 与原因。
- sorted-access 流若不满足非增假设，`threshold_topk` 抛出
  `AssumptionViolatedError`，引擎自动退化为全量参考融合并记录原因。
- `Engine.verify(query, weights, k)` 用**全量融合参考**逐位核验 TA 结果
  （文档序列与分数完全一致），测试中对随机语料、随机权重、多个 k 反复核验。
- 权重为 0 的字段被整体移出激活集：不参与排名、不产生任何访问。
- 未知字段名、NaN 权重直接报错（不可退化）。

## 固定快照分页（不重不漏）

- `Engine.snapshot(query, weights)` 冻结完整排名，返回 token；
  `Engine.page(token, offset, limit)` 在冻结列表上切片，逐页拼接等于完整排名，
  无重复无遗漏；`next_offset=None` 表示结束。
- token 未知抛 `SnapshotNotFoundError`；`offset<0`、`limit<=0` 抛 `ValueError`。
- 索引更新后旧快照仍提供冻结数据（基准不被悄悄改变），新搜索显式使用新版本。

## 运行方式

```bash
python3.11 -m fusion.demo      # 运行样例
python3.11 -m unittest discover -v   # 运行测试
```

## 运行样例（实际输出）

```
== 多字段阈值式 TopK 融合 运行样例 ==
查询: '机器学习 machine'   权重: {'title': 2.0, 'body': 1.0, 'notes': 1.5}   k=3

[meta] mode=ta degraded=False stopped_early=True
[meta] 各字段 sorted-access 拉取次数: {'body': 5, 'notes': 2, 'title': 5}
[meta] 停止时上界 bound=0.4 kth_score=0.5333333333333333
[meta] 归一化明确统计: {'body': {'min': 0.0, 'max': 5.0}, 'notes': {'min': 0.0, 'max': 1.0}, 'title': {'min': 0.0, 'max': 5.0}}

[TopK 结果（含分项得分）]
  #1 d5  score=0.6667
       body   w=1.0  raw=5      normalized=1.0000
       notes  w=1.5  raw=0      normalized=0.0000
       title  w=2.0  raw=5      normalized=1.0000
  #2 d1  score=0.5778
       body   w=1.0  raw=5      normalized=1.0000
       notes  w=1.5  raw=0      normalized=0.0000
       title  w=2.0  raw=4      normalized=0.8000
  #3 d2  score=0.5333
       body   w=1.0  raw=4      normalized=0.8000
       notes  w=1.5  缺失         normalized=  --
       title  w=2.0  raw=2      normalized=0.4000

[全量融合参考核验] ok=True mode=ta

[固定快照分页] limit=2
  snapshot token=v1-s1 total=8
  page offset=0: ['d5', 'd1'] next=2
  page offset=2: ['d2', 'd8'] next=4
  page offset=4: ['d3', 'd4'] next=6
  page offset=6: ['d7', 'd6'] next=None

[负权行为]
  严格模式拒绝: negative weights {'body': -0.5} invalidate the TopK upper bound
  退化模式: mode=exhaustive degraded=True
  退化原因: negative weights: upper bound invalid, degraded to exhaustive fusion
  退化结果: [('d5', 1.0), ('d1', 0.6), ('d2', 0.0)]
```

## 测试结果

```
$ python3.11 -m unittest discover
...........................................
----------------------------------------------------------------------
Ran 43 tests in 0.029s

OK
```

覆盖：分词规则（含 UTF-8/CJK/分隔符）、明确统计归一化（全同分防除零、基准
与候选集无关）、缺失字段剔除而非按 0 惩罚、TA 与全量参考在随机语料上逐位
一致、提前停止上界证据、非单调流退化、负权拒绝/退化（含手算期望值）、权重
0 字段不影响排名、固定快照分页不重不漏且跨索引更新冻结、索引更新是显式新
归一化基准、分项得分输出、确定性重复运行。

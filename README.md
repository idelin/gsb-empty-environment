# Explainable Verifiable Ranking

为排名结果产出**可复算解释树**：每个命中/过滤/剪枝决策都带有匹配词、
权重、统计版本与过滤理由；独立验证器不信任主检索器标签，从原始证据
（语料快照 + 规则快照 + 统计快照）整体重算并逐节点比对，定位第一个
不成立的节点。纯 Python 3.11 标准库实现，无搜索服务、无全文检索库。

## 分词规则（稳定契约）

1. Unicode NFKC 归一化；
2. Unicode casefolding；
3. 从左到右扫描：
   - ASCII 字母/数字的最长连续段（`[a-z0-9]+`）为一个词；
   - 每个 CJK 字符（汉、平/片假名、谚文）单独成词（字级分词）；
   - 其余字符（空白、标点、符号）均为分隔符，不产生词。

## 评分与剪枝公式（验证器按此重算）

```
contribution(t, d) = weight(t) * idf(t) * (1 + ln(tf(t, d)))
score(d)           = fsum(contribution(t, d))   # 对所有命中的查询词
idf(t)             = ln((N + 1) / (df(t) + 1)) + 1
upper_bound(d)     = fsum over ALL query terms of
                     weight(t) * idf(t) * (1 + ln(tf_max(d)))
```

`tf_max(d)` 是文档 d 内的最大词频。因 `tf(t, d) <= tf_max(d)` 且权重非负，
恒有 `upper_bound(d) >= score(d)`，故按上界剪枝是可靠且可复算的。
**解释分项合计（`sum_components`）等于最终分数（`score`）**。

## 解释树结构

- 根节点：查询原文/分词、`corpus_version`、`rules_version`、
  `stats_version`、阈值与 `top_k`。
- 每个文档恰好出现一次（按 `doc_id` 排序），类型为：
  - `hit`：匹配词分项（term/tf/weight/idf/contribution）、分数、
    上界、统计版本、名次；
  - `filtered`：触发的过滤器与具体理由（如 `token_count=2 < min_tokens=3`）；
  - `pruned`：`upper_bound_below_threshold` / `score_below_threshold` /
    `beyond_top_k`，附可靠得分上界与统计版本。
- 每个节点记录 `doc_version` 与 `doc_hash`（SHA-256），文档被替换即可定位。

未命中查询 `explain_miss(tree, doc_id)` 返回具体阶段与可复算数据，
绝不只给笼统的“无结果”。

## 版本语义

- 规则、统计、语料均按版本存入 `RuleStore` / `StatsStore` / `CorpusStore`；
- 规则或统计更新后，旧解释仍绑定旧版本，只要旧快照保留就保持历史有效；
- 缺失统计证据（或规则/语料/文档证据）时，验证结论只能是 `unverifiable`，
  而不是 `failed` 或 `verified`。

## 验证器结论

- `verified`：解释树与独立重算完全一致；
- `failed`：报告第一个不成立节点（`node_id` + `field`），可检测注入篡改
  权重、遗漏过滤节点、替换文档版本、伪造命中标签等；
- `unverifiable`：历史证据缺失，无法核验。

## 运行

```sh
python3.11 demo.py                                  # 端到端演示
python3.11 -m unittest discover -s tests -t . -v    # 28 个单元测试
```

## 目录结构

- `explainable_ranking/tokenizer.py` — 分词器
- `explainable_ranking/corpus.py` — 文档与语料快照存储
- `explainable_ranking/stats.py` — 版本化词项统计（df/idf）
- `explainable_ranking/rules.py` — 版本化规则（权重/过滤器/阈值）
- `explainable_ranking/ranker.py` — 排名流水线 + 解释树 + 未命中解释
- `explainable_ranking/verifier.py` — 独立验证器
- `demo.py` — 运行样例
- `tests/test_explainable_ranking.py` — unittest 测试

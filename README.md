# 可复算解释树的排名系统

纯 Python 3.11 标准库实现（不使用任何搜索服务或全文检索库），文档为本地合成
UTF-8 文本。检索器为每个排名结果产出**可复算解释树**，独立验证器不信任检索器
的任何标签，仅凭版本化证据复算命中结果并核验未命中理由。

## 目录结构

```
ranker/
  tokenizer.py     分词规则（写清、版本化）
  rules.py         评分规则注册表（分词器 / idf 公式按 ID 版本化）
  store.py         版本化存储：文档库、统计快照库、规则库
  explanation.py   解释树节点、规范顺序、Merkle 哈希
  engine.py        主检索器：打分、过滤、剪枝、解释生成、未命中查询
  verifier.py      独立验证器：仅凭证据复算
demo.py            运行样例（覆盖全部场景）
tests/             unittest 测试（33 个用例）
```

## 分词规则（unicode-unigram-v1）

1. 输入为 UTF-8 解码后的 `str`；
2. 先做 Unicode NFC 规范化，再整体 `lower()`（大小写不敏感）；
3. 从左到右逐字符扫描：
   - ASCII 字母或数字 `[a-z0-9]` 累积进当前 token；
   - 其余 Unicode 字母数字字符（`str.isalnum()` 为真，如 CJK 汉字）各自成为
     **单字 token**（unigram），并冲刷当前累积的 ASCII 串；
   - 其它字符（空白、标点、符号）一律视为分隔符；
4. 输出保序 token 列表，重复保留（用于计算 tf）。

例：`"Python3.11 机器学习!"` → `["python3", "11", "机", "器", "学", "习"]`

## 评分公式与解释分项

```
contribution(t, d) = query_weight(t) * tf(t, d) * idf(t)
score(d)           = Σ_t contribution(t, d)        # 按 term 字典序求和
idf(t)             = ln((N + 1) / (df + 1)) + 1    # 规则 v1；v2 为 BM25 风格
```

每个命中文档的解释节点携带全部匹配词分项（词、权重、tf、idf、分项值），
**分项合计严格等于最终分数**（验证器按相同求和顺序复算，浮点逐位一致）。

## 剪枝的可靠得分上界

查询词按 `tub(t) = weight * idf * tf_max(t)` 降序处理；处理到第 i 个词时：

```
ub = 已处理分项之和 + Σ 剩余词的 tub
```

因为 `tf(t, d) <= tf_max(t)`，`ub` 是该文档最终分数的**可靠上界**。当堆已满
（k 个精确分数）且 `ub` 不足以进入 top-k（含 doc_id 平局规则）时剪枝。
剪枝节点记录：已处理分项、剩余上界分项（含统计证据 tf_max）、`upper_bound`、
当时阈值与 kth 文档；统计版本记录在根节点。验证器用统计快照重算上界，
并核对它确实低于最终 top-k 阈值。

## 版本绑定与历史有效

- 解释树根节点绑定 `rules_version` / `stats_version` / `corpus_version`，
  每个文档节点绑定 `doc_version` + 内容哈希；
- 统计变更、规则更新、文档正常升级后，**旧解释仍绑定旧版本复算，保持历史有效**；
- 统计快照本身也可复算：验证器用语料快照独立重算 N/df/tf_max 并逐项比对；
- 缺统计快照 / 规则版本 / 文档历史等证据时，只能判 **unverifiable**，绝不判 invalid。

## 验证判定

- `valid`：全部节点按规范顺序复算通过；
- `invalid`：报告**首个不成立节点**（规范顺序下路径最小者）及理由；
- `unverifiable`：证据缺失。

注入篡改（攻击者重算哈希掩盖痕迹）仍可定位：篡改权重 → 命中词分项节点；
遗漏过滤节点 → 根节点文档账目；替换文档历史版本 → 结果节点文档哈希复算。

## 运行

```bash
python3.11 demo.py                              # 运行样例
python3.11 -m unittest discover -s tests -v     # 测试
```

## 运行样例（节选）

```
========================================================================
1) 检索并产出解释树
========================================================================
  rules_version=1  stats_version=1  corpus_version=10  k=3
  解释树根哈希: 0a50de6031485f01...
  命中 rank=1 doc=d1 score=20.423901
    分项: python(2.0*2*1.7885=7.1538) + 习(1.0*2*1.4520=2.9040) + 器(1.0*2*1.7885=3.5769) + 学(1.0*2*1.6061=3.2123) + 机(1.0*2*1.7885=3.5769)
    分项合计=20.423901  等于最终分数: True
  剪枝节点样例 doc=d9: upper_bound=6.480885 <= threshold=6.635036 (kth=d7), 统计版本=1

2) 指定文档未命中原因查询（不是笼统的“无结果”）
  d4: {"status": "filtered", "reason": "字段 year=2019 不满足过滤条件 year >= 2021", ...}
  d6: {"status": "no_match", "absent_terms": ["python", "习", "器", "学", "机"], ...}
  d9: {"status": "pruned", "upper_bound": 6.4808..., "threshold": 6.6350..., "stats_version": 1, ...}
  d10: {"status": "scored_out", "score": 1.4519..., "threshold": 6.6350..., ...}

3) 独立验证器复算当前解释
  [原始解释] 判定: valid

4) 统计变更 + 规则更新后，旧解释保持历史有效
  新解释绑定: rules_version=2 stats_version=2 corpus_version=11
  旧解释绑定: rules_version=1 stats_version=1 corpus_version=10
  [统计/规则更新后的旧解释] 判定: valid
  [新规则下的新解释] 判定: valid

5) 注入篡改（攻击者会重算哈希掩盖痕迹，验证器靠复算抓出）
  [篡改权重] 判定: invalid
    首个不成立节点: root/result#1(d1)/term(python)
    理由: 权重 99.0 与查询记录 2.0 不一致（疑似篡改）
  [遗漏过滤节点] 判定: invalid
    首个不成立节点: root
    理由: 文档账目不完整：缺少 ['d4']，多余 []
  [文档正常升级后验证旧解释] 判定: valid
  [历史文档内容被篡改] 判定: invalid
    首个不成立节点: root/result#1(d1)
    理由: 文档哈希不匹配：文档版本被替换或篡改

6) 缺统计证据 -> 只能判不可验证
  [统计快照被清除后的旧解释] 判定: unverifiable
    理由: 统计快照版本 1 缺失：缺少统计证据，只能判不可验证
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
...
test_tampered_weight_locates_term_node ... ok
test_omitted_filtered_node_breaks_accounting ... ok
test_replaced_doc_history_locates_result_node ... ok
test_stats_update_keeps_old_explanation_valid ... ok
test_rules_update_keeps_old_explanation_valid ... ok
test_missing_stats_snapshot_is_unverifiable ... ok
...
Ran 33 tests in 0.809s

OK
```

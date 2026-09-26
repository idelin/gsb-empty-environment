# paginated-search

固定索引版本 + 排序键（keyset）分页的最小检索引擎。Python 3.11+，仅标准库，
不依赖任何搜索服务或全文检索库；文档为本地合成 UTF-8 文本。

## 设计

- **固定索引版本**：每次 `SearchEngine.update()` 生成新的版本号，查询与游标都
  绑定到具体版本，翻页期间视图不变。
- **排序键分页**：排序键为 `(-score, doc_id)`——评分降序，同分文档按 ID 升序，
  构成全序。游标记录「最后一条的排序键」，续读时取严格大于该键的结果，
  因此不重复、不漏项；页大小不写入游标，改变页大小不会重读已读文档。
- **游标绑定查询摘要**：摘要 = 规范化查询词元集合 + 排序规则的 SHA-256。
  换查询复用游标抛出 `CursorQueryMismatch`；游标为 base64url(JSON) 不透明串。
- **更新策略二选一**：
  - 快照保持（默认）：`update(docs)` 保留旧版本快照，旧游标继续在旧版本上翻页；
  - 明确失效：`update(docs, invalidate_previous=True)` 或 `invalidate(version)`，
    旧游标再次使用抛出 `CursorInvalidated`。
- **匹配语义**：查询词元 AND 匹配，评分为各词元词频之和。

## 分词规则（tokenizer.py）

1. 输入为本地合成的 UTF-8 文本（`str`）。
2. 规范化：先 Unicode NFKC，再 `str.casefold()` 大小写折叠。
3. 逐字符扫描生成词元：
   - 连续的非 CJK 字母数字（`str.isalnum()` 为真且不在 CJK 区间）构成整体词元，
     如 `python3`、`café`；
   - CJK 统一表意文字（U+3400..U+4DBF、U+4E00..U+9FFF）逐字成单字词元，
     相邻两个 CJK 字额外生成二元词元（bigram），如「分页」→ `分`、`页`、`分页`。
4. 其余字符（空白、标点、符号）均为分隔符，不产生词元。

索引与查询使用同一套规则。

## 文件

- `paginated_search/tokenizer.py` — 分词
- `paginated_search/engine.py` — 版本/快照管理、游标编解码、排序键分页
- `tests/test_engine.py` — unittest 测试（16 个用例）
- `demo.py` — 运行样例

## 运行样例（`python3 demo.py`）

```text
索引版本: v1

== 1. 排序键分页（页大小 2，doc-01 评分最高，同分按 ID 全序） ==
第1页: v1 -> [('doc-01', 9), ('doc-05', 4)]  next_cursor=有
第2页: v1 -> [('doc-06', 4), ('doc-02', 3)]  next_cursor=有
第3页: v1 -> [('doc-03', 3), ('doc-04', 3)]  next_cursor=无

== 2. 页大小中途改变（2 -> 3 -> 1），不重读已读文档 ==
页大小2: v1 -> [('doc-01', 9), ('doc-05', 4)]  next_cursor=有
改页大小3: v1 -> [('doc-06', 4), ('doc-02', 3), ('doc-03', 3)]  next_cursor=有
改页大小1: v1 -> [('doc-04', 3)]  next_cursor=无
已读 6 条，去重后 6 条 -> 无重复无漏项

== 3. 换查询复用游标 -> 拒绝 ==
CursorQueryMismatch: 游标绑定的查询摘要与当前查询不一致，拒绝复用

== 4. 更新索引：默认保留快照，旧游标继续读旧版本 ==
索引版本: v2（新增 doc-07）
旧游标续读(v1快照): v1 -> [('doc-06', 4), ('doc-02', 3)]  next_cursor=有
新查询(v2): v2 -> [('doc-01', 9), ('doc-05', 4), ('doc-06', 4), ('doc-02', 3), ('doc-03', 3), ('doc-04', 3), ('doc-07', 3)]  next_cursor=无

== 5. 更新索引：明确失效，旧游标被拒绝 ==
索引版本: v3（invalidate_previous=True）
CursorInvalidated: 游标绑定的索引版本 v2 已失效，请重新发起查询
```

## 测试结果（`python3 -m unittest discover -s tests -v`）

```text
test_cursor_rejected_for_different_query ... ok
test_cursor_rejected_for_equivalent_query_reworded ... ok
test_empty_result_has_no_cursor ... ok
test_malformed_cursor ... ok
test_no_duplicate_no_missing_across_pages ... ok
test_page_size_change_does_not_repeat ... ok
test_total_order_tie_break_by_doc_id ... ok
test_ascii_alnum_runs ... ok
test_casefold_and_nfkc ... ok
test_cjk_unigram_and_bigram ... ok
test_mixed_text ... ok
test_punctuation_is_separator ... ok
test_duplicate_doc_id_rejected ... ok
test_explicit_invalidation_rejects_old_cursor ... ok
test_invalidate_specific_version ... ok
test_snapshot_retained_after_update ... ok

----------------------------------------------------------------------
Ran 16 tests in 0.001s

OK
```

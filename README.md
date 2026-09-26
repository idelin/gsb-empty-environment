# facetlib — 位图分面统计引擎（纯标准库）

对**主查询结果**做多值属性分面统计，支持当前分面**自排除**（disjunctive
faceting）。Python 3.11+，只用标准库；文档为本地合成 UTF-8 文本，
**不使用任何搜索服务或全文检索库**。

## 分词规则（确定性的，写死如下）

1. 输入为已按 UTF-8 解码的 `str`；
2. 全文做 `str.casefold()` 大小写折叠；
3. Unicode 类别以 `L` 开头（一切字母）或等于 `Nd`（十进制数字）的字符是“词字符”；
4. 汉字（CJK 统一表意文字及扩展、兼容表意文字）**逐字**成一个 token（中文无空格分词）；
5. 其余词字符的极大连续段成一个 token；
6. 空白、标点、符号等都是分隔符，不产生 token。

查询词项同样按此规则分词；一个词项若分出多个 token，则这些 token 以 AND 生效；
分不出 token 的词项抛 `ValueError`。

## 语义（显式约定）

- **主查询** `TextQuery(all_of, any_of, none_of)`：全包含 AND / 任一 OR / 排除 NOT。
- **同一维度内**选中的值之间是 **OR**；**不同维度之间**是 **AND**。
- **自排除**：维度 D 的计数范围 `context(D) = 主查询结果 AND 其余各维度的过滤`，
  不含 D 自身的选中值。同时选中两个分面时，各自计数范围互为对方过滤后的集合，
  例如 B 场景里 `brand` 的计数在 `color=red` 范围内、`color` 的计数在
  `brand∈{Apple,Samsung}` 范围内。
- **缺失 ≠ 空集合**：属性键不存在是 `MISSING`，键存在但值为空列表是 `EMPTY`，
  两列分开统计，也都可以作为选中值参与过滤。
- **同文档同值只计一次**：每篇文档一个比特位，索引时用 `set()` 去重，
  重复标签不会双计；所有计数天然按文档身份去重。
- 计数永远以主查询结果为交集，**不会包含不匹配主查询的文档**。

## 不重扫全库：共享中间结果

索引是 `int` 位图（bit i = 第 i 篇文档）。一次 `compute()` 中：

- 主查询基底位图只算一次，所有维度复用；
- 每个维度的过滤位图只算一次，其余维度的 context 通过**前缀/后缀 AND 积**拼出；
- 每个词项/取值位图最多从索引取一次，重复请求走缓存并计入 `cache_hits`；
- 计数路径**不触碰任何文档正文**，`Metrics.docs_examined` 恒为 0。

`Metrics` 报告：`bitmap_lookups`（实际取数）、`cache_hits`（复用命中）、
`bitmap_ops`（位图运算次数）、`docs_examined`（文档访问量）。

## 增量更新与历史快照

`DocumentStore.upsert/delete` 增量维护位图；位图是不可变 `int`，
`snapshot()` 只是索引字典的浅拷贝——旧快照继续引用旧位图，
因此**历史快照零成本冻结**，新版本互不影响（见 demo 场景 D/E）。

## 正确性核对

`facetlib/reference.py` 是逐文档布尔参考实现：遍历每篇文档、用纯布尔
求值主查询和各维过滤后直接计数，与位图引擎不共享任何计数逻辑。
`tests/test_facets.py` 在 43 篇随机语料（含重复标签、缺失、空集合）上
枚举 **4 种主查询 × 7³ 种选择组合 = 1372 组**，逐一断言引擎与参考完全一致。

## 运行

```bash
python3 demo.py                          # 运行样例（场景 A–F）
python3 -m unittest discover -s tests -t .   # 17 个测试
```

## 文件

- `facetlib/tokenizer.py` — 分词规则
- `facetlib/model.py` — Document / TextQuery / MISSING / EMPTY
- `facetlib/store.py` — 位图索引、增量更新、快照
- `facetlib/engine.py` — 自排除分面引擎 + Metrics
- `facetlib/reference.py` — 逐文档布尔参考（核对用）
- `demo.py` — 运行样例；`tests/test_facets.py` — unittest

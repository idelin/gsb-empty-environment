# 整数区间属性索引（相交 / 包含 / 被包含）

对本地合成 UTF-8 文本文档的整数区间属性建立三种关系索引，支持开放（无界）
端点与属性缺失，更新文档时删除旧索引项。仅使用 Python 3.11 标准库与
`unittest`，不使用任何搜索服务或全文检索库。

## 文档格式

UTF-8 纯文本：头部若干行 `键: 值`，空行后为正文。区间属性的键为 `range`，
没有该行或冒号后为空白即表示属性缺失；`id` 缺省时用文件名（去扩展名）。

```
id: doc-a
range: [0,5)
title: 示例

正文内容……
```

## 分词规则（固定、无检索库）

正文分词仅用于演示/校验，索引不依赖正文分词：

1. 先做 Unicode NFKC 兼容分解（全角字母/数字转半角）；
2. 中日韩表意文字（U+4E00–9FFF、U+3400–4DBF、U+20000–2A6DF、
   U+F900–FAFF）按单字成词；
3. 其余连续的 Unicode 字母/数字/下划线序列成一个 token；
4. 空白与标点（含中文标点 `，。！？` 及括号）作为分隔符丢弃；
5. 保留原大小写，不归一化；纯数字也是 token。

例：`tokenize('区间[0,5) 与 ABC_42')` →
`['区', '间', '0', '5', '与', 'ABC_42']`

## 区间语义与约定

区间写在整数全集 Z 上，端点必须为十进制整数或留空（留空表示该方向无界）：

| 写法 | 归一化整数集合 |
|---|---|
| `[0,5)` | `{0,1,2,3,4}`（右端开，减 1） |
| `[5,8)` | `{5,6,7}` |
| `(0,5]` | `{1,2,3,4,5}`（左端开，加 1） |
| `[3,)` / `(,0]` / `(,)` | 右无界 / 左无界 / 全 Z |
| `[5,5]` | 点区间，只含整数 `5` |
| `[5,5)`、`(5,5]`、`(5,5)` | 空区间（不含任何整数） |
| 字段缺失或空白 | “缺失”，不是区间，仅可被缺失查询选中 |

三种关系（空区间与缺失均不参与任何匹配）：

- 相交 intersects：`a.lo <= b.hi 且 b.lo <= a.hi`，故 `[0,5)` 与 `[5,8)`
  归一化为 `[0,4]` 与 `[5,7]`，**不相交**；
- 包含 contains：查询区间包含文档区间；
- 被包含 contained_by：查询区间被文档区间包含。

## 索引结构与更新

`interval_index/catalog.py` 用 `bisect` 维护两个有序列表（每篇文档在每个
列表中至多一项，故结果天然无重复 id）：

- `(lo, hi, id)` 按 lo 排序——三种查询都可只扫一侧端点的候选段；
- `(hi, lo, id)` 按 hi 排序——对称裁剪/完整性校验；
- 无界端用哨兵整数表示（`lo=-10**18`、`hi=10**18`），缺失进 `missing`
  集合，空区间进 `empty` 集合，不进有序列表。

`upsert` 时先用旧区间从两个列表摘除旧行（或从 missing/empty 集合移除），
再插入新区间；因此更新后不会残留旧索引项。查询结果统一按 id 排序返回。

## 代码结构

```
interval_index/interval.py   区间解析、归一化、三种关系判定
interval_index/document.py   UTF-8 文档解析与分词
interval_index/catalog.py    双端有序索引、upsert/remove/query/full_scan
sample_docs/                 9 篇合成样例（含无界、缺失、空区间、点区间）
demo.py                      运行样例
tests/                       34 个 unittest 用例（含随机一致性测试）
```

## 运行样例

```
$ python3.11 demo.py
已装载 9 篇文档；索引统计: {'documents': 9, 'lo_rows': 7, 'hi_rows': 7, 'missing': 1, 'empty': 1}
属性缺失文档: ['doc-g']
空区间文档  : ['doc-h']

查询 [0,5)
  相交 intersects                    -> ['doc-a', 'doc-c', 'doc-e', 'doc-f']
  查询包含文档 contains              -> ['doc-a']
  查询被文档包含 contained_by        -> ['doc-a', 'doc-f']

查询 [5,8)
  相交 intersects                    -> ['doc-b', 'doc-c', 'doc-f', 'doc-p']
  查询包含文档 contains              -> ['doc-b', 'doc-p']
  查询被文档包含 contained_by        -> ['doc-b', 'doc-f']

查询 [5,5]
  相交 intersects                    -> ['doc-b', 'doc-c', 'doc-f', 'doc-p']
  查询包含文档 contains              -> ['doc-p']
  查询被文档包含 contained_by        -> ['doc-b', 'doc-c', 'doc-f', 'doc-p']

更新 doc-a: [0,5) -> [100,200]
  更新后索引统计: {'documents': 9, 'lo_rows': 7, 'hi_rows': 7, 'missing': 1, 'empty': 1}
  [0,5) 相交 -> ['doc-c', 'doc-e', 'doc-f']（不再含 doc-a）
  [150,150] 相交 -> ['doc-a', 'doc-d', 'doc-f']
```

样例同时对每次查询断言 `query == full_scan` 且 id 无重复。

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
...（共 34 个用例）...
test_adjacent_half_open_disjoint ... ok
test_update_deletes_old_index_entries ... ok
test_update_interval_to_missing_and_back ... ok
test_random_upserts_queries_updates ... ok
test_cjk_is_single_character_tokens ... ok
test_half_open_integer_normalization ... ok
test_point_intervals_convention ... ok
...
----------------------------------------------------------------------
Ran 34 tests in 0.399s

OK
```

覆盖要点：`[0,5)` 与 `[5,8)` 不相交；点区间 `[x,x]` 合法而 `[x,x)` 为空；
开放端点归一化与无界端；缺失/空区间不匹配；更新后旧行删除、行数不增长、
旧端点不残留；删除文档；600 轮随机文档/更新下三种查询与全扫描逐一一致
且无重复 id。

# phraseindex — 含词位置的倒排表（纯 Python 3.11 标准库）

本地合成 UTF-8 文本上的位置倒排索引：精确短语、可配置间隔短语、同义位置图，
返回全部最小覆盖范围与位置证据；支持索引段合并、文档撤回、快照游标；
匹配按位置跳跃（block-max skipping），不做全笛卡尔积；附带独立的逐文档
暴力枚举核验器，并记录解码块数。不使用任何搜索服务或全文检索库。

## 分词规则（`phraseindex/tokenizer.py`）

1. 输入为 UTF-8 解码后的 `str`。
2. 词元 = Unicode 字母数字码点（`str.isalnum()`）的**最长连续序列**；
   空白、标点、emoji 等均为分隔符。因此 `你好` 是一个词元，`你好🙂world`
   切成 `你好` / `world`。
3. 词元用 `str.casefold()` 归一化，匹配大小写不敏感。
4. `start`/`end` 是**原始字符串的码点偏移**（左闭右开），
   `text[start:end]` 即原文表面形式。
5. 位置按（文档， 字段）从 0 编号；**短语不能跨字段拼接**。

## 查询语义（`phraseindex/query.py`）

- 查询 = 元素序列；每个元素是一个词或同义词集合（同义位置图）。
- `gaps[i]` 限定第 `i` 与第 `i+1` 个元素之间最多间隔的词元数：
  `0` 为精确相邻（精确短语），`g` 允许中间至多 `g` 个词。可整体一个
  整数，也可逐对配置。
- 匹配位置严格递增，因此**同一词位置不能复用**于两个查询项：
  短语 `a a` 必须消耗两个不同位置。
- 每个匹配返回**最小覆盖范围**（首末命中词元的原文偏移）与逐元素
  位置证据（元素序号、命中词、位置、偏移）。

## 索引结构（`phraseindex/index.py`, `phraseindex/postings.py`）

- 索引 = 若干不可变段 + 撤回墓碑集合。`add_documents` 一批一段；
  `merge()` 压实为单段并物理清除已撤回文档；`delete_document` 打墓碑，
  位置与文档频率（df）都只对存活文档计算，二者天然同步。
- `snapshot()` 捕获当前段与墓碑的不可变视图；快照上的搜索/游标不受
  后续增删影响。`search(..., after_doc=...)` 以 doc_id 为续传令牌分页。
- 位置按块存储（默认 8 个/块），块最大值是免费元数据；匹配器用
  block-max 跳过不相关块，只解码相交块，`DecodeCounter` 记录解码块数。

## 独立核验（`phraseindex/verify.py`）

`brute_force_field` 直接从文档词元序列做显式笛卡尔枚举再过滤（与索引
匹配器完全独立）；`verify_snapshot` 对快照中每个存活文档逐字段对照，
并汇总解码块数与扫描位置数（含极高频重复词用例）。

## 运行

```sh
python3.11 demo.py            # 运行样例
python3.11 -m unittest -v     # 测试（19 个用例）
```

## 运行样例（节选）

```
== exact phrase [quick brown fox] ==
  d1.body range=[4:19] text='quick brown fox' positions=(1, 2, 3)
    evidence: q0->quick@pos1[4:9], q1->brown@pos2[10:15], q2->fox@pos3[16:19]

== repeated term [a a] (needs 2 distinct positions) ==
  d2.body range=[0:3] text='a a' positions=(0, 1)
  d2.body range=[2:5] text='a a' positions=(1, 2)
  ...

== UTF-8 phrase [中文分词测试 quick] ==
  d4.body range=[15:28] text='中文分词测试, quick' positions=(3, 4)

== document frequency before/after retraction ==
  df(quick) = 4
  delete d3 -> df(quick) = 3

== high-frequency repeated term (decode block accounting) ==
  'a a' over 500 x 'a': matches=499 DecodeCounter(blocks_decoded=562, positions_scanned=999)
  verification: ok=True expected=499 actual=499
```

## 测试结果

```
$ python3.11 -m unittest -v
...
Ran 19 tests in 0.181s
OK
[high-freq 'a a' x500] matches=499 blocks_decoded=562 positions_scanned=999
[skip check 'a b'] positions_scanned=501 blocks_decoded=64
```

`skip check`：500 个 `a` 后接 500 个 `b`，查询 `a b`（相邻）只扫描
501 个位置、解码 64 块即得唯一边界匹配 `(499, 500)`——线性而非
500×500 的笛卡尔积。

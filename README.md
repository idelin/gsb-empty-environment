# invidx：含词位置的倒排表与短语查询

纯 Python 标准库实现（3.11+），不使用任何搜索服务或全文检索库。
文档为本地合成 UTF-8 文本，由调用方按 UTF-8 读入为 `str`。

## 分词规则（invidx/tokenize.py）

- 词条 = Unicode 字母/数字（`str.isalnum()`）的最长连续序列；
  空白、标点、下划线、emoji 等一律是分隔符。
- 规范化：`str.casefold()` 小写折叠，索引与查询同一规则。
- 位置：每个字段内部从 0 开始逐词 +1；字段间位置空间独立，
  因此**跨字段不能拼短语**。
- 每个词条记录原文字符区间 `[start, end)`，命中可映射回原文范围。

## 功能

- **倒排表**：`term -> doc -> field -> 分块位置表`（块大小 8）。
- **短语查询**：同义位置图（每槽位多个备选，备选可为多词序列，
  如 `new york ~ nyc`）、可配置间隔 `slop`（相邻槽位空隙总和上限，
  0 为精确短语）、重复词必须占用不同位置（`a a` 需要两个位置）。
- **结果**：返回全部最小覆盖范围（丢弃严格包含其他命中的区间），
  每条命中带原文字符范围与逐槽位位置证据；同一词位置不会被
  两个查询项复用（有序不重叠约束保证）。
- **按位置跳跃**：匹配器以“当前文档最稀有槽位”为锚点向两侧按窗口
  扩张，位置表按块二分跳跃、只解码重叠块，避免全笛卡尔积；
  每次查询记录解码块数（`DecodeStats`）。
- **段与维护**：写缓冲 `commit` 成不可变段；`delete` 登记墓碑，
  位置与文档频率在读取侧同步失效；`merge` 压实段并物理移除已删
  文档；`snapshot` 固定一致性视图（其后的提交/删除/合并不影响
  快照）；`cursor` 在快照上按 docid 翻页并可凭位置恢复。
- **独立核验**：`invidx/verify.py` 不经过倒排表，逐文档在词条序列
  上暴力枚举（自带独立的最小覆盖实现），与索引结果对照；
  含 4 万次重复词的极高频案例会记录解码块数。

## 运行

```bash
python3 -m unittest discover -s tests -v   # 测试
python3 examples/demo.py                   # 样例
```

## 样例输出（节选）

```
== synonym graph: (new york | nyc) subway ==
  doc=news-2 field=body pos[1,3] char[4,19) text='new york subway' | slot0=new/york@(1, 2), slot1=subway@(3,)
  doc=news-2 field=body pos[8,9] char[37,47) text='nyc subway' | slot0=nyc@(8,), slot1=subway@(9,)

== repeated term: a a (needs two positions) ==
  doc=blog-1 field=body pos[10,11] char[41,44) text='a a' | slot0=a@(10,), slot1=a@(11,)
  doc=blog-1 field=body pos[11,12] char[43,46) text='a a' | slot0=a@(11,), slot1=a@(12,)

before delete: df(quick)=2 df(a)=1
after delete blog-1: df(quick)=1 df(a)=0

== high-frequency term x (40000 occurrences, 5000 blocks) ==
  hit pos[19999,20001]
  blocks_decoded=3 (y:1, x:2), positions_scanned=3
```

## 测试结果

`python3 -m unittest discover -s tests`：31 个用例全部通过，覆盖
分词规则、精确/间隔短语、同义位置图、重复词位置不复用、最小覆盖
范围、跨字段隔离、删除后位置与文档频率同步、段合并、快照游标，
以及 300+ 组随机文档/查询的逐文档暴力枚举对照；极高频重复词案例
中 5000 个位置块仅解码 3 块。


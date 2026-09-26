# 分段索引多字段 TopK 归并（Python 3.11 标准库）

在本地合成的 UTF-8 文本上构建**分段倒排索引**，按**多个排序字段**做跨段 **TopK** 归并，
支持字段升降序与 `NULL` 的 FIRST/LAST 位置，业务过滤后继续补足候选，
并通过删除位图排除不可见文档。**只使用 Python 3.11 标准库与 `unittest`，
不使用任何搜索服务或全文检索库。**

## 目录结构

```
minisearch/
  __init__.py    包入口
  tokenizer.py   UTF-8 文本分词器（re + unicodedata）
  document.py    Document 数据模型
  sort.py        SortField / RankKey：升降序、null 位置、同分稳定
  segment.py     段：倒排链、删除位图、段内全量候选（不做前K截断）
  index.py       跨段 k 路堆归并 + 过滤后补足
  corpus.py      本地合成语料（24 条中英混合文档，3 个段）
demo.py          运行样例（8 个场景）
tests/           unittest 测试（26 个）
SAMPLE_RUN.txt   demo.py 实际输出
TEST_RESULTS.txt 测试实际结果
```

## 分词规则

输入为 `str`（UTF-8 解码后的文本），字节串不隐式解码。规则固定如下：

1. Unicode **NFKC** 规范化（全角 `ＵＳＢ` → `usb`）；
2. **casefold** 大小写归一（`ß` → `ss`）；
3. 下划线视为分隔符（`keyboard_42` → `keyboard`、`42`）；
4. 用正则 `[^\W_]+` 切出字母/数字连续段；
5. 段内 CJK 字符（假名、CJK 统一表意文字等）**逐字成词**，
   其余非 CJK 字符合并为一个 token（拉丁字母、数字等）。

示例：

```
"Wireless 机械键盘 keyboard_42 ßtraße ＵＳＢ"
-> ['wireless', '机', '械', '键', '盘', 'keyboard', '42', 'sstrasse', 'usb']
```

查询词按上述规则分词后做 **OR** 匹配；`query=None` 表示匹配全部可见文档。
相关度 `_score` = Σ (1 + ln(tf)) · (ln((N+1)/(df+1)) + 1)，
其中 N、df 只统计未删除文档，删除会改变 IDF。

## 排序语义

- `SortField(name, descending=False, nulls=None)`：`nulls` 缺省遵循 SQL 约定——
  升序 `NULLS LAST`、降序 `NULLS FIRST`，也可显式传 `NULLS_FIRST/LAST`；
- 多字段按声明顺序依次比较；
- 所有排序字段都相同时，**按 doc_id 升序**稳定决胜（`RankKey` 同时供
  `list.sort` 与 `heapq` 使用）；
- 排序字段类型不可比较时抛 `TypeError`，不做隐式强转；
- `_score` 是引擎注入的伪字段，默认排序即 `_score` 降序 + ID 升序。

## 归并算法（为什么不能只取各段前 K 个原始项）

1. 每个段用 `IndexSegment.iter_ranked` 产出该段**全部**命中候选，段内已按同一套
   多字段规范排序，**不在段内截断**；
2. `Index.search` 用小顶堆做 k 路归并，堆顶永远是全局最靠前的未消费候选；
3. 弹出后立即从其所属段拉下一个补堆：某段排序最靠前的文档（段头）即使被业务
   过滤器全部排除，也会**继续读取该段**后续候选；
4. 过滤器在归并阶段施加，被过滤候选不占 TopK 名额，结果在剩余候选中补满 K；
   候选确实耗尽时返回少于 K 条（或空），而不是丢弃候选。

样例 `SAMPLE_RUN.txt` 场景 1 给出对照：K=2、价格升序、排除促销时，
"每段只取前 K 个原始项再过滤" 得到 `[1, 18]`，本实现得到正确的 `[1, 6]`。

## 删除位图

`Index.delete(doc_id)` 在所属段的 `deleted` 集合上打标记（位图语义）：
不可见文档不出现在倒排链匹配、相关度统计（N/df）和归并结果中，
且会被后续候选自动补足位置；`is_deleted` 可查询，段 `live_count` 实时反映。

## 运行方式

需要 Python 3.11（仅标准库，无第三方依赖）：

```bash
python3.11 demo.py                       # 运行样例
python3.11 -m unittest discover -s tests # 跑全部测试
python3.11 -m unittest discover -s tests -v  # 详细模式
```

最小 API 用法：

```python
from minisearch import Document, Index, SortField, NULLS_LAST

index = Index()
index.commit([
    Document(1, "mechanical keyboard 机械键盘",
             {"price": 120, "rating": 4.7}),
    Document(2, "wireless keyboard 无线键盘",
             {"price": 90, "rating": None}),
])
index.delete(2)  # 删除位图：doc 2 变为不可见
hits = index.search(
    "keyboard",
    sort_by=[SortField("rating", descending=True, nulls=NULLS_LAST),
             SortField("price")],
    limit=5,
    doc_filter=lambda doc: doc.fields["price"] >= 100,
)
```

## 测试结果

`python3.11 -m unittest discover -s tests`：**26 个测试全部通过**，
完整输出见 `TEST_RESULTS.txt`。覆盖：分词各规则、倒排链、删除位图与 IDF 变化、
段内候选不截断、多字段升/降序、null FIRST/LAST 与默认位置、同分 ID 稳定、
段头全被过滤时继续读取、过滤后补足（含与暴力枚举/错误做法的对照）、
删除不可见与补足、空查询与候选耗尽、参数校验。

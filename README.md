# 分段索引 TopK 归并

纯 Python 3.11 标准库实现（不使用任何搜索服务或全文检索库）：从多个
索引段按**多个排序字段**归并 TopK，支持每个字段独立的**升降序**与
**null 位置**，**过滤后自动补足候选**，支持**删除位图**，同分按
**文档 ID 稳定**。

## 分词规则

文档为本地合成的 UTF-8 文本，分词器见 `topk/tokenizer.py`：

1. 输入为已解码的 `str`；`None` 视为空文本。
2. 先做 Unicode **NFKC 规范化**：全角字母/数字/符号折叠为半角。
3. 再整体**小写化**（`str.lower`），大小写不敏感。
4. 切分：
   - 连续 ASCII 字母数字 `[a-z0-9]+` 为一个 token（英文单词、数字）；
   - 每个 CJK 表意文字（`\u4e00-\u9fff`）单独为一个 token（单字 unigram）；
   - 其余字符（空白、中英文标点、emoji 等）一律为分隔符，丢弃。
5. 查询词与文档走**完全相同**的分词流程。

示例：`"ＴＯＰＫ索引归并"` → `["topk", "索", "引", "归", "并"]`

## 归并设计

- **段内有序**：段不可变，`Segment.cursor()` 按排序比较器产出可见
  文档的惰性游标（命中删除位图的文档直接跳过，不计入结果）。
- **K 路归并**：堆中保存每段当前队首，按比较器（含 doc_id 兜底，
  构成全序）弹出最小者。
- **过滤后补足**：过滤判定发生在弹出阶段。候选被过滤时不占 TopK
  名额，立即从该段读取下一条补足；段头整段被过滤时自然持续向后
  读取，直到集满 K 条或所有段耗尽。**不会**"先取各段前 K 条原始项
  再过滤"——那种做法在过滤命中率高时会漏掉本应入选的文档。
- **排序规格**：`SortField(name, desc, nulls_first)`，null 位置与
  升降序相互独立（同 SQL `NULLS FIRST/LAST`）；缺失字段视为 null；
  全部字段相同（同分）时按 `doc_id` 升序稳定。

## 目录结构

```
topk/
  tokenizer.py   # 分词器（NFKC + 小写 + 英文词/数字 + CJK 单字）
  sortspec.py    # 排序规格与比较器（升降序、null 位置、ID 稳定）
  filters.py     # 过滤条件（字段比较、token 匹配、& | ~ 组合）
  segment.py     # 索引段 + 删除位图 + 有序游标
  merge.py       # K 路归并 TopK（流式过滤补足）
demo.py          # 运行样例
tests/           # unittest 测试（34 例）
```

## 运行

```bash
python3 demo.py                  # 运行样例
python3 -m unittest discover -s tests -v   # 运行测试
```

## 样例输出

```
== 分词示例 ==
ＴＯＰＫ索引归并 -> ['topk', '索', '引', '归', '并']

== 排序规格 ==
  score DESC NULLS LAST
  updated ASC NULLS FIRST
  (同分按 doc_id 升序稳定)
过滤条件: Filter((lang == 'zh' & body CONTAINS ['引', '索']))

== Top 5 结果 ==
  #1 id=301 score=96 updated=None body=索引段头过滤演示
  #2 id=201 score=96 updated=2024-06-01 body=索引归并与过滤补足
  #3 id=104 score=96 updated=2024-06-04 body=分段索引的归并排序
  #4 id=105 score=95 updated=None body=索引与删除位图
  #5 id=203 score=93 updated=2024-06-03 body=ＴＯＰＫ索引归并

说明：seg-a 头部 101/102/103 虽分数最高，但被过滤后直接跳过并
继续向后读取补足；106/303 命中删除位图，不参与归并。
```

## 测试结果

```
Ran 34 tests in 0.002s
OK
```

覆盖：分词规则（NFKC/小写/CJK 单字/分隔符）、多字段混合升降序、
null 位置及其与方向的独立性、同分 ID 稳定、K 路归并顺序、过滤后
补足（段头整段被过滤仍继续读取）、token 过滤、删除位图不可见、
k=0 / k 超总量 / 空段等边界。

# normsearch — 规范化检索 + UTF-16 原文范围高亮

纯 Python 3.11 标准库实现（`unicodedata` / `html` / `unittest`，无搜索服务、
无全文检索库）。本地合成 UTF-8 文本，解码为 `str` 后处理。

## 规范化流水线（逐字符、产生可组合的多对多映射）

对每个原文 Python 字符独立处理（`normsearch/normalize.py`）：

1. **删除控制符/格式符**：Unicode 类别 `Cc`、`Cf`（如 `\x00`、软连字符
   `U+00AD`、零宽空格 `U+200B`）——产出 0 个规范化字符。
2. **大小写折叠**：`str.casefold()`，含扩展，如 `ß → ss`、`İ → i + U+0307`。
3. **兼容分解**：`unicodedata.normalize("NFKD", ...)`，如 `é → e + U+0301`、
   连字 `ﬁ → fi`。
4. **丢弃组合记号**：规范组合类非 0 的字符（重音等），使 `café == cafe`。

一个原文符可能产出多个规范化字符（扩展/分解），也可能产出零个（被删除）。
因此**不能按字符长度差平移位置**。实现为每个规范化输出字符记录其来源原文的
**UTF-16 码元半开区间** `(start, end)`（星面层字符如 😀 占 2 个码元）。
多个相邻输出可共享同一来源区间（多对一）；规范化区间 `[a,b)` 映射为
`spans[a][0] .. spans[b-1][1]`（多对多取并集，天然可组合）。被删除控制符
不产生条目，后续字符的偏移不会错移。

## 分词规则（`Index.iter_tokens`）

在**规范化后**的串上：token = 规范化字符中类别首字母为 `L`（字母）或 `N`
（数字）的极大连续段；其余字符（空格、标点、符号）均为分隔符。注意控制符
已在规范化阶段删除，所以 token 可以跨越软连字符，例如 `ﬁsch­brötchen`
（ﬁ 分解、­ 删除、ö 去重音）→ 单个 token `fischbrotchen`。

检索本身在规范化串上做包含全部重叠出现的子串匹配；`search(query)` 返回
全局 **UTF-16 码元区间**列表（含重叠匹配）。

## 索引段与版本隔离（`normsearch/index.py`）

- 一个 `Index` 绑定一个 `doc_version`；混入其他版本段抛
  `VersionMismatchError`——旧版索引段永远不能与新版原文混配。
- 段按加入顺序拼接（段间**不插入分隔符**），全局 UTF-16 基址由之前各段
  UTF-16 长度累加；跨段匹配（可切在词中间）保留一个完整区间。

## HTML 高亮（`normsearch/highlight.py`）

- `merge_ranges` 合并重叠/相邻区间；
- 渲染前对原文做 `html.escape`（`< > & "` 全转义），再插入 `<mark>`，
  故原文中的 `<script>...</script>` 以 `&lt;script&gt;` 出现，不可能作为标签执行；
- 区间按 UTF-16 边界表转成 Python 串索引，星面层字符的代理对永远不会被切开；
- 去掉 `<mark>` 标签并 unescape 可还原出逐字符相同的原文（可追溯性）。

## 运行

```sh
/opt/homebrew/bin/python3.11 -m unittest discover -s tests -v
/opt/homebrew/bin/python3.11 demo.py
```

## 随机核验（`tests/test_property.py`）

固定种子随机生成含 `ß é É ﬁ İ 组合记号 软连字符 零宽空格 控制符 引号
< > & 星面层字符` 的串，与一个**独立朴素实现**对照：

- 朴素实现用 `char.encode("utf-16-le")` 字节数独立计算 UTF-16 宽度，用自己
  的循环重建规范化串与来源区间表，与生产实现逐一比对；
- 每个来源区间按 UTF-16-LE 字节切回必须恰好解码为完整字符；
- 随机规范化子串作查询：映射区间等于来源区间并集，且该原文片段重新规范化
  后包含查询；
- 随机原始子串作查询渲染 HTML：无 `<script`/`onerror`，去标签 unescape 后
  逐字符还原原文，所有区间在 UTF-16-LE 字节层面是合法字符范围；
- 文档在任意字符位置随机切两段，跨段结果与整文档结果完全一致。

## 文件

- `normsearch/normalize.py` — 规范化与多对多 UTF-16 来源映射
- `normsearch/index.py` — 版本化索引段、拼接、搜索、分词
- `normsearch/highlight.py` — 区间合并、边界转换、转义渲染
- `tests/` — 单元测试 + 随机属性测试（共 22 个测试）
- `demo.py` — 合成文档运行样例

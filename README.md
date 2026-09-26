# 纯标准库布尔检索引擎

Python 3.11+，仅使用标准库，不依赖任何搜索服务或全文检索库。
文档为本地合成的 UTF-8 文本，以 `doc_id -> {字段名: 文本}` 的形式载入内存索引。

## 分词规则（索引与查询共用）

1. 文本一律按 UTF-8 解码（Python `str`）。
2. ASCII 字母与数字的连续序列 `[A-Za-z0-9]+` 构成一个词元，统一转小写。
3. 每个 CJK 字符（汉字、平/片假名、韩文音节，按 Unicode 字符名识别）
   单独成为一个单字词元，不做词典分词。
4. 其余所有字符（空白、标点、符号、下划线等）均为分隔符，不产生词元。

因此查询词项 `编程` 会被切成 `编`、`程` 两个词元，按 AND 处理；
`Python3.11` 会被切成 `python3`、`11`。

## 查询语言

- 运算符：`AND`、`OR`、`NOT`（大小写不敏感），以及括号 `()`。
- 优先级：`NOT` > `AND` > `OR`。例如 `NOT A OR B` 按 `(NOT A) OR B` 求值。
- 相邻词项之间为隐式 `AND`：`python 编程` 等价于 `python AND 编程`。
- 字段限定：`field:term`，或作用于括号组 `field:(a OR b)`，
  字段限定会传播到子树内所有词项。
- 保留字限制：`and`/`or`/`not` 无法作为普通词项被搜索。

文法：

```
query    := or_expr EOF
or_expr  := and_expr (OR and_expr)*
and_expr := unary ((AND)? unary)*
unary    := NOT unary | primary
primary  := '(' or_expr ')' | TERM ':' primary | TERM
```

## 语义约定

- **NOT 基于固定文档全集**：`NOT q` = 当前全集 − q 的命中集。
  纯否定查询（如 `NOT python`）可以独立执行。
- **删除即移出全集**：`delete_document` 后，文档不再出现在任何结果中，
  包括 NOT 的补集。
- **缺失字段不等于空串**：文档缺少某字段时，该字段上的限定谓词为假，
  而不是按空串参与匹配；因此 `NOT field:x` 会命中缺失该字段的文档。
  字段存在但为空串的行为与缺失一致（正向不匹配、否定命中），
  但二者在引擎内部是区分的：空串字段不会索引任何词元。
- **多词元词项**：无字段限定时，所有词元须出现在文档任意字段
  （不要求同一字段）；有字段限定时，所有词元须出现在该字段内。

## 命中证据

每个结果携带 `evidence` 列表，元素为 `(field, term, count)`：
命中的字段、词元及其在该字段中的出现次数。
纯否定分支命中的文档没有正向证据（evidence 为空列表）。

## 文件

- `search_engine.py` — 分词、查询解析（递归下降）、倒排索引与求值
- `test_search_engine.py` — unittest 测试（38 例）
- `demo.py` — 运行样例

## 运行

```sh
python3 -m unittest test_search_engine -v   # 运行测试
python3 demo.py                             # 运行样例
```

# boolsearch

纯 Python 3.11 标准库实现的布尔检索引擎：解析 AND / OR / NOT / 括号 / 字段限定，
对本地合成 UTF-8 文本文档建内存倒排索引，结果带命中证据。不依赖任何搜索服务或全文检索库。

## 分词规则

1. 文本按原样扫描记录字符偏移，词项再做 `str.casefold()` 大小写折叠归一化；
2. CJK 字符（中日韩表意文字、日文假名、韩文音节）**逐字成词**（中文无空格分词）；
3. 其余连续的字母 / 数字 / 下划线构成一个词；
4. 其他字符（空白、标点等）一律视为分隔符；
5. 每个词记录原文中的字符偏移 `[start, end)`，用于生成命中证据。

## 查询语法

```ebnf
query    := or_expr
or_expr  := and_expr (OR and_expr)*
and_expr := not_expr (AND not_expr)*
not_expr := NOT not_expr | primary
primary  := '(' or_expr ')' | [FIELD ':'] TERM
```

- 优先级：**NOT > AND > OR**，因此 `NOT A OR B` 按定义解析为 `(NOT A) OR B`；
- `AND` / `OR` / `NOT` 必须大写，小写形式按普通词项处理；
- 无隐式 AND：相邻词项（如 `a b`）是语法错误；
- `TERM` 为不含空白、括号、冒号的非空字符串，查询时同样做 casefold；
- `FIELD` 与文档字段名**精确匹配**（区分大小写）；不限定字段时匹配任意字段。

## 语义要点

- **NOT 基于固定文档全集**：`NOT q` = 当前全集 − `q` 的命中集，纯否定查询（如 `NOT python`）可正确执行；
- **删除文档即从全集移除**：`delete_document` 同时清理倒排索引，后续 NOT 的补集不再包含它；
- **缺失字段 ≠ 空字符串**：文档只保存显式给出的字段；缺失字段与空串字段都不产生分词、永不命中词项查询，但二者是可区分的状态；
- **命中证据**：每个命中文档附带 `(字段, 词项, 偏移, 上下文片段)` 列表；由 NOT 命中的文档无正向证据（证据列表为空）。

## 运行

```sh
python3.11 demo.py                 # 运行样例
python3.11 -m unittest test_boolsearch -v   # 运行测试（27 个用例）
```

## 文件

- `boolsearch.py` — 分词、查询解析（递归下降）、索引、求值与证据
- `test_boolsearch.py` — unittest 测试套件
- `demo.py` — 运行样例

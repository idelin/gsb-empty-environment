# 固定索引版本的键集分页查询引擎

在本地合成 UTF-8 文档上提供 AND 语义查询与游标分页。**纯 Python 3.11
标准库实现，不使用任何搜索服务或全文检索库**；测试基于 `unittest`。

## 设计要点

- **固定索引版本**：每次文档增删使索引版本号 +1 并生成不可变快照；
  一次分页会话的所有请求都绑定同一版本，版本内结果集不变。
- **键集（keyset）分页**：排序键为 `(-score, doc_id)`，`score` 为查询
  词项词频之和；同分文档按 `doc_id` 字典序排列，构成**全序**，因此
  翻页不重复、不漏项。游标记录上一页最后一条的排序键 `[score, doc_id]`。
- **游标绑定查询摘要**：游标内含规范化查询（分词结果 + 排序规格）的
  SHA-256 摘要与索引版本号，并用 HMAC-SHA256 签名防篡改；换查询复用
  游标抛出 `CursorQueryMismatchError`，版本已失效抛出
  `CursorIndexExpiredError`，篡改抛出 `CursorTamperedError`。
- **页大小可安全变化**：页大小不写入游标，翻页位置完全由排序键决定，
  中途改变页大小不会重复已读文档。
- **更新策略二选一**（`UpdatePolicy`）：
  - `SNAPSHOT` 快照保持：保留旧版本快照，旧游标可继续翻页，读到的是
    更新前的数据（调用方需自行权衡内存占用）；
  - `INVALIDATE` 明确失效：只保留最新版本，旧游标立即报错。

## 分词规则（查询与文档一致）

1. 输入为 UTF-8 解码后的 `str`；
2. 全文做 Unicode 大小写折叠（`str.casefold()`）；
3. CJK 字符（统一表意文字及扩展 A 区、平假名、片假名、谚文）逐字成词；
4. 其余连续字母/数字（`str.isalnum()`）合并为一个词；
5. 空白、标点等其他字符均为分隔符，不产生词。

## 文件结构

- `search_engine/tokenizer.py` 分词器
- `search_engine/cursor.py`   游标编解码、查询摘要、异常类型
- `search_engine/engine.py`   索引快照、更新策略、分页查询
- `tests/`                    unittest 测试
- `demo.py`                   运行样例

## 运行

```sh
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```

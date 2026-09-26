# 不可变段 + 删除位图文本索引（LSM 风格）

纯 Python 3.11 标准库实现的小型文本索引，不使用任何搜索服务或全文检索库
（无 Elasticsearch / Lucene / Whoosh 等依赖）。

## 设计

- **不可变段**：每次 `flush()` 把内存写冻结成一个 `Segment`，内含文档记录与
  倒排表（term → 文档位图），写入后内容永不变。
- **删除位图（live docs）**：每段一个 Python `int` 位图，1=可见。新版本写入
  新段后重算各段位图，使旧版本/被删文档位置 0（遮蔽），旧数据仍在段中。
- **读快照（MVCC）**：`open_snapshot()` 冻结打开时刻的段列表与每段位图副本。
  位图是不可变 `int`，后台重算产生新 int，旧读者继续查旧段、旧位图。
  被合并替换的旧段保留到所有快照释放后才可回收。
- **后台合并**：`merge()` 在锁外构建新段、锁内原子连续替换；合并期间允许
  `upsert/flush`，新段追加在尾部。构建计划记录范围外的更新文档，发布时若某
  文档已被更新的段持有，合并结果不带该文档，由全局位图重算保证**新值不丢**。
- **墓碑保留规则**：合并时，墓碑仅当合并范围外仍存在版本更旧的同 ID 记录
  （它还需要遮蔽该段）时才带入新段；范围外已无更旧段即可安全丢弃。

## 分词规则（`lsm_index/tokenizer.py`，索引与查询一致）

1. `bytes` 输入按 UTF-8 严格解码，非法字节抛 `UnicodeDecodeError`。
2. 统一 `str.casefold()` 做 Unicode 大小写折叠。
3. CJK 表意文字（U+4E00–9FFF、U+3400–4DBF）逐字切分，一字一 token。
4. 其余连续 `isalnum()` 字符组成一个 token；标点/空白只作分隔符。
5. 不去停用词、不做词干提取；纯函数、确定性。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 20 个测试
python3.11 run_sample.py                       # 运行样例（输出见 SAMPLE_RUN.txt）
```

## 模块

- `lsm_index/tokenizer.py` — UTF-8/CJK 分词
- `lsm_index/segment.py` — 不可变段、倒排位图、删除位图、可见词频统计
- `lsm_index/index.py` — 写入/flush、快照、连续段合并（含确定性后台合并封装）
- `lsm_index/corpus.py` — 本地合成的 12 篇中英混合 UTF-8 文档
- `tests/test_index.py` — unittest 测试

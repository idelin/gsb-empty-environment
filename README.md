# 有序文档变更索引管道（纯标准库）

接收带 `seq` 的文档变更事件，支持乱序缓冲、checkpoint、原子批次发布、
幂等去重与可重启持久化。文档为本地合成的 UTF-8 文本，分词与倒排索引
均自行实现，**不使用任何搜索服务或全文检索库**。Python 3.11+，仅标准库。

## 目录结构

```
indexing/
  events.py     # 事件模型：seq / doc_id / op(upsert|delete) / text / version
  tokenizer.py  # 分词规则实现（见下）
  buffer.py     # 乱序缓冲 SeqBuffer：缺号检测与显式报告
  index.py      # 倒排索引：原子批次提交、版本幂等、快照读
  pipeline.py   # 管道：去重 -> 重排 -> 原子提交 -> 持久化
demo.py         # 端到端运行样例
tests/          # unittest 测试（25 个）
```

## 分词规则（tokenizer.py）

1. 输入为 `str`；`bytes` 按 UTF-8 严格解码（非法字节直接抛错）。
2. ASCII 字母与数字组成最长连续串 `[A-Za-z0-9]+`，统一转小写
   （`Hello` -> `hello`，`py3.11` -> `py3`、`11`）。
3. 每个 CJK 统一表意文字是独立的单字词（unigram），覆盖区间
   U+3400–U+4DBF、U+4E00–U+9FFF、U+F900–U+FAFF。
4. 其余一切字符（空白、标点、符号、下划线）均为分隔符，不产生词元。

注意：CJK 为单字词，因此检索词也是单字（如搜 `支`、`持`），不支持短语查询。

## 语义保证

- **顺序**：事件按全局 `seq`（从 1 开始、不允许跳号）严格升序应用；
  乱序到达的事件进入 `SeqBuffer`，缺口补齐后按序整批释放。
- **缺 seq 显式报告**：`submit` 返回值与 `pipeline.missing_seqs()` 给出
  闭区间缺口列表（如 `[(6, 7)]`），绝不静默跳过。
- **原子批次**：`commit()` 将本次释放的事件在同一把锁内整体应用，
  `commit_version` 只递增一次；`snapshot()`/`search()` 读者只能看到
  批次提交前或提交后的完整状态（有并发测试覆盖）。
- **重复事件幂等**：`seq <= checkpoint`、已在待提交批次或已在缓冲区的
  事件直接判重丢弃并计数；索引应用本身按"文档版本严格更大才生效"
  （版本缺省取 `seq`），且词频由文档文本**推导重置**而非累加，
  因此重放/重启不会重复增加词频。
- **删除先到但 seq 较新**：删除事件在缓冲区等待缺口补齐后按 seq 顺序
  应用（upsert 先、delete 后）；删除会留下墓碑（tombstone），声明版本
  更旧的 upsert 无法复活文档，声明版本更新的 upsert 可正常覆盖。
- **重启不重复计数**：checkpoint、待提交批次、乱序缓冲区、索引全部
  持久化为 JSON（临时文件 + `os.replace` 原子写）；重启后重放旧事件
  一律判重丢弃。提交时先写索引再写 checkpoint，两者间崩溃也安全——
  事件仍在待提交批次中，重新应用是版本幂等的空操作。

## 运行

```bash
python3 demo.py                          # 端到端样例（状态写入 demo_state/）
python3 -m unittest discover -s tests -v # 运行测试
```

## 运行样例输出（python3 demo.py）

```
== 1. Tokenization (UTF-8, local synthetic docs) ==
  'Hello world, hello Python 世界' -> ['hello', 'world', 'hello', 'python', '世', '界']
  'Python 3.11 支持原子提交吗？支持！' -> ['python', '3', '11', '支', '持', '原', '子', '提', '交', '吗', '支', '持']

== 2. Out-of-order submit: seq=2 arrives before seq=1 ==
  submit seq=2 -> buffered, missing=((1, 1),)
  submit seq=1 -> drained, drained=2, missing=()

== 3. Duplicate event is dropped (idempotent) ==
  re-submit seq=1 -> duplicate

== 4. Newer delete (seq=4) arrives before seq=3: it waits ==
  submit delete seq=4 -> buffered, missing=((3, 3),)
  submit seq=3 -> drained, drained=2
  commit -> committed=4, checkpoint=4, commit_version=1
  search('hello') -> {}
  search('python') -> {'doc-2': 1}
  search('支') -> {'doc-2': 2}

== 5. Newer upsert for doc-1 (seq=5) brings it back ==
  commit -> committed=1, checkpoint=5
  search('hello') -> {'doc-1': 2}
  search('世') -> {'doc-1': 1}

== 6. Restart: state reloads, frequencies unchanged, replays dropped ==
  stats before restart: {'checkpoint': 5, 'commit_version': 2, 'pending_batch': 0, 'buffered': [], 'duplicates_dropped': 1, 'docs': 3}
  stats after  restart: {'checkpoint': 5, 'commit_version': 2, 'pending_batch': 0, 'buffered': [], 'duplicates_dropped': 1, 'docs': 3}
  replay seq=3 after restart -> duplicate
  search('hello') -> {'doc-1': 2}
  OK: term frequencies identical after restart

== 7. Missing seq is reported explicitly ==
  submit seq=8 while checkpoint=5 -> buffered, missing=((6, 7),)
  pipeline.missing_seqs() -> [(6, 7)]
```

## 测试结果（python3 -m unittest discover -s tests）

```
Ran 25 tests in 0.068s

OK
```

覆盖：分词规则（ASCII/CJK/UTF-8 解码/非法字节）、乱序缓冲与缺号报告、
重复事件幂等、删除先到等待与墓碑、声明版本覆盖、原子批次（含多线程
读者一致性）、快照隔离、更新替换词频、重启后状态恢复与重放去重、
崩溃前未提交批次的恢复。

## 已知限制

- 单分区全局 `seq`；多分区需要每分区独立 checkpoint。
- 墓碑不做回收（演示规模下可忽略）。
- 持久化为整体 JSON 快照，未做增量 WAL。

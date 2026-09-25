# 带版本局部字典的可溢写批次合并

纯 Python 3.11 标准库实现，不使用查询引擎或第三方依赖。输入是带列类型的字典编码批次；输出为一个物理发布字典、每批次每列的重映射表和一个重编码批次。

## 关键语义

- 局部码没有全局含义：批 0 的码 `0` 与批 1 的码 `0` 通过各自版本和列名分别解释。
- 稳定身份是 `term_id`；同值但不同 `term_id` 会保留为两个发布词项。
- 已发布目录按“首个引用的全局行、批次、局部码”确定，重复运行结果一致。
- 未被任何非空行引用的词项不会进入发布字典。
- `None` 只存在于 null 位图；空字符串、NaN 都是字典值，NaN 按 IEEE-754 位模式比较。
- 整数以十进制文本物理编码，不用浮点数，保留任意精度整数。
- 局部字典版本不可变；旧批次重编码只使用其绑定版本的重映射表。撤回归档到新版本，不修改旧版本。
- 无效码和已撤回引用都报告批内 1 起始行号和全局 1 起始行号。
- 外部排序和所有输出写入受逻辑内存预算约束；单个不可分记录是允许超过预算的最小例外。
- 发布先写入事务目录，再用一次目录原子替换发布；已发布重跑返回同一代结果。
- 随机块乱序到达、取消后恢复，与“直接解码再串接”的参考逐值相等。

## 运行

```bash
python3.11 example_run.py
python3.11 -m unittest discover -s tests -v
```

示例输出见 `sample_output.txt`，当前环境测试结果见 `test_results.txt`。

## 主要 API

- `LocalDictionary(version, {Column: [TermSpec(...)]})`：带版本的不可变局部字典。
- `DictBatch`：由类型化列、局部字典和非负整数码组成；`None` 表示 null。
- `split_batch_random(batch, seed=...)`：随机切分并可乱序返回块。
- `MergeJob(workdir, batches, memory_limit_bytes=...)`：持久化合并作业。
- `MergeJob.add_chunk(chunk)`：写入块并原子更新清单。
- `MergeJob.cancel()` / `MergeJob.resume(workdir)`：取消和恢复。
- `MergeJob.finalize()`：生成物理字典、重映射表、重编码批次和字节记账。
- `CollisionSafeMap(hash_func=lambda key: 0)`：用强制哈希碰撞验证稳定性身份不串项。

## 物理产物

- `published/generation-0001/dictionary.pdic`：发布字典。
- `published/generation-0001/batch.rcod`：带每列 null 位图的重编码批次。
- `published/generation-0001/remaps/*.rmap`：每批次、每列、绑定局部版本的局部码到发布码映射。
- `published/generation-0001/release.json`：行数、SHA-256 和记账结果。

记账字段包括发布字典字节、重映射表字节、重编码批次字节、累计溢写字节、已删除临时字节、剩余临时字节和峰值逻辑内存。

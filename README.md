# extsort — 有限内存外排序游标

基于 Python 3.11 标准库（仅用 `argparse`/`hashlib`/`heapq`/`json`/`os` 等）实现的外部归并排序。
记录为 **键、原输入序号、值** 三元组，按 `(key, seq)` 排序，因此同键记录严格保持原输入顺序（稳定排序）。

## 输入 / 输出格式

- **输入**：JSON Lines，每行一个对象 `{"key": K, "value": V}`。
  - `K` 必须可相互比较（全为数值或全为字符串）；`V` 为任意 JSON 值，缺省为 `null`。
  - 记录的 `seq` 由输入行号（0 起）自动赋予，不由输入提供。
- **输出**：JSON Lines，每行 `{"key": K, "seq": I, "value": V}`，按 `(key, seq)` 升序。
- **临时段（run）**：工作目录内的 `run-RRR-NNNNNN.run`，每行 `[K, I, V]`；
  清单 `manifest.json` 记录每个段的记录数与 SHA-256，并自带负载校验和。

## 命令行

```sh
python -m extsort INPUT OUTPUT [--workdir DIR] [--memory N] [--handle-budget N] [--keep-work]
```

- `--memory N`：内存预算，以允许缓存的记录数计（每段在内存中排序的记录上限），默认 10000。
- `--handle-budget N`：文件句柄预算；合并扇入 = `N - 2`（为输出文件与清单临时文件各保留 1 个句柄），默认 32。
- `--workdir DIR`：临时段与清单目录，默认 `<OUTPUT>.extsort-work`。
- `--keep-work`：成功后保留临时文件（调试用）。
- 退出码：`0` 成功；`1` 输入/参数错误；`2` 清单或临时段校验失败（损坏状态，拒绝继续）。

中断后直接以相同命令重新运行即可从清单恢复。

## 示例（合成数据）

```sh
printf '{"key": 2, "value": "b"}\n{"key": 1, "value": "a"}\n{"key": 2, "value": "c"}\n' > in.jsonl
python -m extsort in.jsonl out.jsonl --memory 2 --handle-budget 4
cat out.jsonl
# {"key":1,"seq":1,"value":"a"}
# {"key":2,"seq":0,"value":"b"}
# {"key":2,"seq":2,"value":"c"}   <- 同键保持输入顺序 b 在 c 前
```

## 可靠性与恢复设计

- 每个临时段先写 `.tmp`、fsync、`os.replace` 原子发布，然后计算 SHA-256 并原子更新清单。
- 清单记录：已读记录数、输入字节偏移、已读输入前缀 SHA-256、合并轮次、各段的 `{path, count, sha256}`。
- 恢复时：校验清单自身校验和 → 逐段重算哈希与行数 → 校验段记录数之和等于已读输入数。
  任何不匹配抛出 `CorruptRunError` / `ManifestCorruptError`，**损坏段绝不静默丢弃**。
- 分段阶段记录输入偏移与前缀哈希；恢复时若输入已变化则拒绝继续。
- 不变式：任意时刻 `Σ(段记录数) == 已读输入记录数`；最终输出前再次核对，不等则删除临时输出并报错。
- 最终输出写入同目录临时文件、fsync、核对记录数后 `os.replace` **原子替换**目标。
- 因为段一旦发布即不可变、输出替换是原子的，恢复重跑不会重复增加记录数。

## 测试

```sh
python -m unittest discover -s tests -v
```

覆盖：空/单条输入、随机数据对照 Python 稳定排序、三条同键记录在单记录分段与多轮合并下的顺序保持、
分段/合并阶段中断恢复、恢复后无重复记录、损坏段/缺失段/损坏清单/输入被篡改的拒绝、CLI 端到端。
所有测试数据均为临时目录内生成的合成数据。

## 限制（未证明最优之处）

- 分段采用定长块排序，未实现置换选择（replacement-selection），段数非理论最少。
- 扇入固定为 `handle_budget - 2`，未做按段数的自适应扇入或合并轮次优化。
- 记录以 JSONL 文本存储，I/O 与体积非最优；未做压缩。
- 键要求同类型可比较；混合类型键会抛出 `TypeError`。
- 合并阶段假设堆中每条记录常驻内存开销可忽略（仅受句柄预算约束），未对超长单条记录做额外限制。

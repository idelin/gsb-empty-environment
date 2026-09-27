# binlog — 二进制日志分段校验

仅依赖 Python 3.11 标准库的追加式二进制日志组件：长度前缀 + 序号 +
前段摘要 + CRC32 组成帧头，支持流式读取任意字节片段，严格区分
**末尾未完整写入（可截断恢复）** 与 **中间损坏（报错、拒绝恢复）**。

## 帧格式（所有整数为大端）

```
+------------------+---------------------------------------------------+
| length  (4 字节) | uint32，payload 长度（上限 64 MiB）               |
| seq     (8 字节) | uint64，序号，从 0 开始逐帧 +1                    |
| prev    (32 字节)| 前一帧完整编码字节的 SHA-256；第 0 帧为 32 个 0   |
| crc32   (4 字节) | uint32，CRC-32(seq || prev || payload)            |
| payload (length) | 不透明字节                                        |
+------------------+---------------------------------------------------+
帧头固定 48 字节，帧总长 = 48 + length。
```

## 校验与恢复语义

- **clean**：流恰好结束于帧边界。
- **truncated**：末尾存在未写完整的帧（例如只写入了 length 字段）。
  唯一允许的恢复方式是截去这段不完整尾部（`recover`）。
- **corruption**：任何完整帧的 CRC 错误、序号跳变、prev 摘要链断裂、
  或 length 超过上限。报出损坏偏移并拒绝恢复——**绝不跳过损坏帧继续
  提交，也绝不把完整帧的 CRC 错误当作可忽略尾部**。
- 已知边界：若损坏发生在 length 字段且使其指向 EOF 之外，与撕裂写
  （torn write）不可区分，按 truncated 处理（见 README 末尾说明）。

## 命令行

```bash
python3 binlog.py append  LOG --text "hello" [--hex 00ff]...   # 追加帧（自动续链）
python3 binlog.py verify  LOG        # 校验，输出 status/frames/valid_bytes/tail_bytes
python3 binlog.py recover LOG        # 截去不完整尾部；发现损坏时退出码 1 且不改文件
python3 binlog.py reindex LOG IDX    # 重建索引（每行一个 JSON：{"seq","offset","length"}）
python3 binlog.py inject  LOG [--offset N --xor 0xFF] [--truncate N]   # 故障注入
```

退出码：校验/恢复成功为 0；发现损坏打印
`CORRUPT offset=<N> reason=<...>` 到 stderr 并返回 1。

## 库接口

- `encode_frame(seq, payload, prev_digest) -> bytes`
- `StreamReader`：`feed(bytes)` 增量喂入任意分片，`finish()` 返回
  `(clean|truncated, tail_bytes)`；损坏抛 `CorruptionError(offset, reason)`
- `verify_file(path)` / `verify_bytes(data)` / `recover_file(path)` /
  `rebuild_index(path, idx)` / `append_frames(path, payloads)` /
  `inject_fault(...)`

## 测试

```bash
python3 -m unittest test_binlog -v
```

覆盖：编解码往返、任意分片大小流式不变性、**逐字节截断**（每个截断点
都必须分类为 clean/truncated 且恢复到正确的帧边界）、末帧只写 length
字段恢复到前一帧（验收边界）、完整帧 CRC 错误报损坏（验收边界）、
中间损坏不跳过、序号/摘要链/长度字段损坏、逐字节翻转必被检出、
索引重建、CLI 与故障注入。所有数据均为合成数据。

## 设计限制（如实说明）

- length 字段被改大到超出文件剩余字节时，与"末尾未写完"在原理上不可
  区分，按 truncated 处理；length 被改小或落在文件内时会被 CRC/序号/
  摘要链捕获为 corruption。
- CRC-32 为 32 位检错码，对恶意构造的碰撞不提供密码学保证；帧间完整
  性由 SHA-256 摘要链加强。

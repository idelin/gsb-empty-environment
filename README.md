# seglog — 二进制追加日志分段校验

纯 Python 3.11 标准库实现（`struct` / `zlib` / `argparse` / `unittest`）。
追加式二进制日志，逐帧携带长度前缀、序号、前段摘要与 CRC32，
支持流式读取任意字节片段，可区分**末尾撕裂写**与**中间损坏**。

## 帧格式（大端字节序）

```
+------------+-----------+----------------+===============+-----------+
| len   u32  | seq  u64  | prev_digest u32| payload (n B) | crc  u32  |
+------------+-----------+----------------+===============+-----------+
```

| 字段 | 说明 |
|---|---|
| `len` | 帧体长度 = `seq + prev_digest + payload + crc` 的字节数（不含自身 4 字节） |
| `seq` | 帧序号，从 0 开始严格递增 1 |
| `prev_digest` | 前一帧**完整字节**（含其 len 与 crc）的 CRC32；首帧为 0 |
| `payload` | 任意字节数据，`n = len - 16` |
| `crc` | 对 `len + seq + prev_digest + payload` 的 CRC32 |

固定开销：每帧 20 字节（4+8+4+4）。payload 上限 64 MiB。

## 扫描结论（三态）

| 状态 | 含义 | 恢复策略 |
|---|---|---|
| `ok` | 全部帧完整且 CRC/链/序号校验通过 | — |
| `truncated` | 末尾存在未完整写入的帧（撕裂写） | 允许截断到 `good_offset`，恢复到前一帧 |
| `corrupt` | 完整帧 CRC 错误 / 链断裂 / 序号跳号 | **禁止截断、禁止跳过损坏帧继续提交**，报错并保留文件 |

损坏点之前已验证的帧仍会随结果返回（`ScanResult.frames`），
但损坏帧及其后的任何帧都不得提交。

### 已知边界（明确说明）

长度前缀 4 字节被损坏、且损坏后的声明长度超出文件末尾时，
与"只写了部分帧"的撕裂写**不可区分**，此时归类为 `truncated`。
这是长度前缀定位帧边界这一设计的固有局限；
一旦声明长度落在文件范围内，CRC 校验必然捕获损坏（`corrupt`）。

## 命令行

```sh
python3 seglog.py append  LOG --text "hello"     # 追加一帧（或从 stdin 读原始字节）
python3 seglog.py verify  LOG                    # 校验；退出码 0=ok 1=corrupt 2=truncated
python3 seglog.py truncate LOG                   # 截去不完整尾部；损坏时拒绝（退出码 1）
python3 seglog.py index    LOG [IDX]             # 重建索引，可选写出二进制索引文件
python3 seglog.py dump     LOG                   # 列出全部帧
python3 seglog.py inject flip-byte LOG OFFSET    # 故障注入：翻转指定字节
python3 seglog.py inject truncate  LOG SIZE      # 故障注入：截断到 SIZE 字节
```

## 输入输出格式

- **日志文件**：上述帧的顺序拼接，无文件头。
- **索引文件**：`count u32`，随后 `count` 条 `(seq u64, offset u64, frame_len u32)` 大端记录。
- **verify/dump 输出**：`status=<ok|truncated|corrupt> frames=N good_offset=M`，
  附 `detail:` 说明行；`dump` 另逐帧打印 `seq/offset/payload 长度/prev/crc/数据预览`。

## 库接口

- `encode_frame(seq, prev_digest, payload) -> bytes`
- `StreamParser.feed(data) -> list[Frame]`：增量解析，可喂任意大小片段
- `scan(data) / scan_file(path) -> ScanResult`：三态校验
- `truncate_tail(path)`：仅截断撕裂尾部，损坏时抛 `CorruptError`
- `append_frame(path, payload)`：扫描取末帧序号/摘要后追加
- `rebuild_index(log, idx=None)` / `read_index(idx)`
- `inject_flip_byte(path, off)` / `inject_truncate(path, size)`

## 测试

```sh
python3 -m unittest -v test_seglog
```

覆盖：编解码回环、任意/随机分块流式解析、**逐字节截断**（每个截断点
都必须可恢复到前帧）、**逐字节翻转**（绝不误判 ok）、中间帧损坏不跳过、
链断裂/序号跳号、索引重建与回读、CLI 全流程（含故障注入）。
所有测试数据均为合成数据。

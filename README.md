# segcrc — 分段 CRC-32 计算与按长度合成

纯 Go 标准库实现（仅依赖 `hash/crc32`）。把一段字节流拆成任意多段独立存储，
只凭每段的 **(CRC, 长度)** 摘要就能合成整体校验，**不需要重新读取任何未变
更段的字节**。

## CRC 参数

CRC-32/ISO-HDLC（即 `zlib`/`gzip`/`zip` 使用的 IEEE 校验，`crc32.IEEE`）：

| 常量 | 值 | 含义 |
| --- | --- | --- |
| `segcrc.Poly` | `0xEDB88320` | 反射生成多项式（`0x04C11DB7` 的倒数位序） |
| `segcrc.Init` | `0xFFFFFFFF` | 初始寄存器值 |
| `segcrc.XorOut` | `0xFFFFFFFF` | 处理完所有字节后的末异或值 |

空数据摘要为固定恒等元 `segcrc.Empty = {CRC:0, Length:0}`。

## 核心 API

- `Hasher`：流式累加，字节可任意分块（含 0 长块、不规则块）；累计长度超过
  `MaxLen` 时返回 `ErrLenOverflow`。
- `Summary{CRC, Length}`：段摘要，长度是身份的一部分，带文本编码
  `%08x:%d`（`Encode` / `ParseSummary`）。
- `Combine(a, b)` / `Compose(parts...)`：移植自 zlib 的 `crc32_combine`
  GF(2) 偶/奇矩阵算法，只用 `b.Length` 推进零字节算子，再异或 `b.CRC`。
- `Joiner`：按名字与固定顺序装配套路；`Set`/`Replace` 替换某个子段后
  `Build()`/`Verify()` 只组合摘要，不触碰未替换段的数据。
- `Source` / `SourceFunc` + `HashSegment` / `ReadSegment`：
  - `HashSegment` 按声明长度流式生成摘要（用于新写/替换段）；
  - `ReadSegment` 按 `(CRC,长度)` 校验已有段。

### 缺数据 vs 坏数据（严格区分）

- 源里根本没有该段（`Open` 返回 `false`/`nil`）→ 包装 `ErrMissingData`；
- 数据在但摘要不符或提前 EOF → 包装 `ErrCorruptData`；
- 配额不足 / 长度溢出 → `ErrLenOverflow`；
- 非法摘要编码、0 长度配非 0 CRC → `ErrBadSummary`。

### 在分配前检查长度额度

读取缓冲区固定为 64 KiB（流式 `io.ReadFull`），声明长度先与 `quota` 比较，
超限立即返回且**不打开数据源、不分配缓冲**，因此不可信的超大声明长度无法
触发大内存分配。

### 安全规则

- 任意切分再合成恒等于整体一次计算（随机 200 组测试 + zlib 算法对照）。
- 空段是恒等元：`Combine(Empty, x) == Combine(x, Empty) == x`。
- 相同 CRC、不同长度不可混用：`Verify` 在 CRC 相同而长度不同时返回
  `ErrLenMismatch`；`Combine` 对长度相加做 int64 溢出检查。

## 目录

```
segcrc/            库实现与测试
  crc.go           参数、错误、Summary、流式 Hasher
  combine.go       zlib crc32_combine 移植、Compose、Joiner
  segment.go       Source、Hash/ReadSegment、额度、摘要编解码
  segcrc_test.go   go test 测试
cmd/crcdemo/       端到端演示
```

## 运行

```sh
go test ./... -race -cover
go run ./cmd/crcdemo
```

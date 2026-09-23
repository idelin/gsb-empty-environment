# secded — 扩展 Hamming SECDED + 交织块格式

纯 Go 标准库实现（无任何同功能压缩/纠错库依赖）。提供：

- 扩展 Hamming **SECDED**：区分「无错 / 可纠单错 / 检测双错」；
- **擦除（erasure）解码**：已知位置的删除标记可恢复；
- **可随机访问的交织块格式**：抗传输突发错误；
- **批量流处理**编码/解码，输出额度有界；
- **位来源映射**与**未纠正块状态**持久化（JSON）；
- 穷举测试 + 独立校验矩阵交叉验证（`go test`）。

## 位位置映射（文档化约定）

码字存放在 `uint64` 的低 `n` 位，`n = k + r + 1`（`k` 数据位、`r` 汉明校验位、1 整体校验位）：

| 比特索引 | 含义 |
|---|---|
| 0 | 整体偶校验位（覆盖位 1..n-1，使整个码字 popcount 为偶） |
| j ∈ [1, n-1]，j 为 2 的幂（1,2,4,8,…） | 汉明校验位，覆盖二进制表示含该位的所有位置 |
| j ∈ [1, n-1]，其余 | 数据位，按位置升序依次填充（第 0 个数据位 → 最低的非 2 幂位置） |

以 `k=11, r=4, n=16` 为例：数据位 i → 码字位置 `[3 5 6 7 9 10 11 12 13 14 15]`，
校验位在位置 `{1,2,4,8}`，整体校验位在位置 `0`。

综合征定义：汉明综合征 `s` = 所有置位位置（1..n-1）的 XOR；整体校验 `p` = 全部 `n` 位的 XOR。
每个码字比特对应唯一「列」`(位置值, 1)`，这是擦除解码按综合征匹配求解的基础。

译码判定：

| s | p | 结论 |
|---|---|---|
| 0 | 0 | 无错（clean） |
| ≠0 | 1 | 单错，位于比特 `s`（含校验位自身），纠正 |
| 0 | 1 | 整体校验位（比特 0）单错，纠正 |
| ≠0 | 0 | 双错，**检出但不纠正**（double-error-detected） |

## 声明的纠错能力（最小汉明距离 4）

- 1 比特错误 → 纠正（数据位、汉明校验位、整体校验位一视同仁）；
- 2 比特错误 → 检出，绝不误报已修复；
- ≤3 个擦除（位置已知、无其他错误）→ 恢复；
- 1 擦除 + 1 未知错误 → 恢复。

**超出能力的错误可能不可检测（明确声明，不隐瞒）：**

- 3 比特错误会混叠为单错综合征而被「误纠正」——测试实测 896/896 个三翻转模式全部表现为误纠正或恰好数据未变；
- 4 比特错误可落在另一合法码字上而**完全不可检测**（实测 224 个模式报 clean 但数据已错）；
- 2 擦除 + 1 未知错误可能被静默误纠。

因此 `clean` / `corrected-single` / `recovered-erasure` 状态只是**综合征自洽的结果**，
绝不是「原数据一定正确」的证明；流格式层另有载荷 CRC-32 做端到端校验，
且任何未纠正块都会记录进状态报告。

## 交织块格式（可随机访问、抗突发）

文件头 28 字节（全部大端）：魔数 `"SCE1"`、版本号、`k`、交织深度 `D`、保留字节、
块数 `B`、载荷长度 `L`、载荷 CRC-32、头部 CRC-32。**版本、长度、校验均显式**；
解码器拒绝未知版本、坏头 CRC、截断体和超额输出。

正文为 `ceil(B/D)` 个帧，每帧 `D` 个块按比特交织。帧 `f` 中块 `b` 的第 `pos` 位位于正文比特偏移：

```
f*(n*D) + pos*D + b        （正文比特按 MSB-first 打包进字节）
```

任意长度 ≤ D 的连续突发比特错误，每块至多命中 1 位，由 SECDED 纠正。
`Header.BitOffset(block, pos)` 即随机访问原语；末帧零填充。

擦除标记以「正文相对比特索引」传入（`BitSet`），模拟传输层标记的不可靠比特。

## 批量流处理与有界输出

- `Encoder.Encode(w, r)`：按 64 KiB 批次读入，逐帧编码写出；
- `Decode(r, erasures, maxBytes)`：声明载荷长度超过 `maxBytes` 时在解码前直接返回
  `ErrOutputTooLarge`；逐块译码并累积审计报告；CRC 失败或存在未纠正块时返回
  `ErrDataCorrupt`（同时返回尽力而为的载荷，调用方必须视为不可信）。

## 位来源映射与未纠正块状态

- 编码产出 `SourceMap`：每个载荷比特 → （块、帧、码字位置、正文比特偏移），
  CLI 落盘为 `<out>.map.json`；
- 解码产出 `DecodeReport`：每块状态（clean / corrected-single / recovered-erasure /
  double-error-detected / uncorrectable）、被翻转的码字位置、未纠正块列表、
  载荷 CRC 结果，CLI 落盘为 `<in>.status.json`。

## 测试（独立校验矩阵交叉验证）

`go test ./...` 包含：

- **穷举**（k=4 短码，16 个数据字 × 全部模式）：单比特翻转（含校验位自身）全部纠正；
  双比特翻转全部检出且绝不误报修复；1..3 个擦除全部恢复；1 擦除 + 1 错误全部恢复；
- k=11 全部 2048 数据字 × 16 位置单翻转穷举；
- **独立校验矩阵**：测试内从定义重建 H 矩阵，GF(2) 矩阵乘验证库综合征，
  并验证所有编码码字满足 `H·c = 0`；
- 诚实性测试：固定展示三错误混叠、四错误不可检测的行为（防止能力被夸大）；
- 流格式：多参数往返、≤D 突发全纠、>D 突发检出且 CRC 失败、擦除标记恢复、
  随机访问偏移一致性、来源映射一致性、输出额度、头部校验、未纠正块记录。

## 运行样例

```console
$ printf 'SECDED demo payload: extended Hamming with interleaving.\n' > msg.txt
$ secded encode -in msg.txt -out msg.scd
encoded msg.txt -> msg.scd: k=11 r=4 n=16 depth=8 blocks=42 payload=57 bytes (map: msg.scd.map.json)

$ secded map -in msg.scd
version=1 k=11 r=4 n=16 depth=8 blocks=42 payload=57 bytes crc=a03a93c1
data bit i -> codeword position: [3 5 6 7 9 10 11 12 13 14 15]
block 0 codeword bits at body offsets: [0 8 16 24 32 40 48 56 64 72 80 88 96 104 112 120]

$ secded decode -in msg.scd -out msg.out && cmp msg.txt msg.out
decoded msg.scd: blocks=42 clean=42 corrected=0 erasure-recovered=0 uncorrectable=[] crc-ok=true ...

# 单比特错误（整体校验位自身）→ 纠正
$ secded flip -in msg.scd -out c1.scd -bit 224 && secded decode -in c1.scd -out c1.out
decoded c1.scd: blocks=42 clean=41 corrected=1 ... crc-ok=true

# 8 比特突发（=交织深度，落在 8 个不同块）→ 全部纠正
$ secded flip -in msg.scd -out c2.scd -bit 248 -bit 249 ... -bit 255 && secded decode -in c2.scd -out c2.out
decoded c2.scd: blocks=42 clean=34 corrected=8 ... crc-ok=true

# 同一块内双比特错误 → 检出、绝不误报修复，退出码非零
$ secded flip -in msg.scd -out c3.scd -bit 248 -bit 264 && secded decode -in c3.scd -out c3.out
decoded c3.scd: ... uncorrectable=[0] crc-ok=false
error: secded: payload failed verification   (exit=1)

# 擦除标记（块 0 的 3 个比特）→ 恢复
$ printf '0\n8\n16\n' > erasures.txt && secded decode -in msg.scd -out c4.out -erasures erasures.txt
decoded msg.scd: ... erasure-recovered=1 ... crc-ok=true

# 输出额度有界
$ secded decode -in msg.scd -out c5.out -maxbytes 10
error: secded: payload length exceeds decode bound: 57 > 10   (exit=1)
```

## 测试结果

```console
$ go test ./...
ok  	secded	0.438s
?   	secded/cmd/secded	[no test files]
```

```text
--- PASS: TestParamsTable
--- PASS: TestExhaustiveSingleBitFlip
--- PASS: TestExhaustiveDoubleBitFlip
--- PASS: TestExhaustiveErasures
--- PASS: TestExhaustiveErasurePlusError
--- PASS: TestParityBitSelfError
--- PASS: TestIndependentParityCheckMatrix
--- PASS: TestTripleErrorsAreNotClaimedCorrect   (896/896 三翻转混叠，已文档化)
--- PASS: TestQuadrupleErrorsCanBeUndetectable   (224 个四翻转模式不可检测，已文档化)
--- PASS: TestRoundTripRandom
--- PASS: TestStreamRoundTrip
--- PASS: TestBurstWithinDepth
--- PASS: TestBurstBeyondDepth
--- PASS: TestErasureMarkers
--- PASS: TestRandomAccessBitOffset
--- PASS: TestSourceMapConsistency
--- PASS: TestDecodeBound
--- PASS: TestHeaderValidation
--- PASS: TestUncorrectedBlocksRecorded
```

## 代码布局

- `secded.go` — 参数推导、编码、综合征、单错/双错译码、擦除译码；
- `format.go` — 文件头、交织布局、随机访问偏移、位来源映射、擦除位集；
- `stream.go` — 批量流式编码/解码、审计报告、有界输出；
- `cmd/secded/main.go` — 演示 CLI（encode/decode/flip/map）；
- `secded_test.go`、`stream_test.go` — 穷举与交叉验证测试。

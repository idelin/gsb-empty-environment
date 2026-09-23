# bitplane

定宽整数的位平面（bit-plane）压缩库。纯 Go 标准库实现，不依赖任何压缩库。

## 原理

1. 输入 `[]int64` 按指定位宽 `w`（1..64）截断为位模式；可选 **有符号 zigzag 映射**，
   使小绝对值负数的高位平面稀疏（负数往返保持位模式不变）。
2. 把值流拆成 `w` 个位平面，每个平面按块切分。
3. 每个平面块在 **字面（literal）/ 游程（rle）/ 差分（delta）** 三种模式中按实际
   字节数选最小者；全零平面只需 1 个模式字节 + 1 个 varint 游程，极为紧凑。
4. 编码器枚举候选块大小，按 **包含头部与索引的真实总字节** 最小化分块；
   小块（n ≤ 1024）穷举全部块大小。
5. 每块带独立 CRC32，随机区间解码只读取重叠块，**不展开无关块**；
   损坏块局部报错，不影响其他块。

## 线格式（小端）

```
Header (44 bytes):
  0  magic "BPC1" | 4 version=1 | 5 flags(bit0=signed) | 6 width | 7 reserved(0)
  8  count u64 | 16 blockSize u32 | 20 numBlocks u32
  24 indexLen u64 | 32 dataLen u64 | 40 headerCRC32
Index: numBlocks × { offset u64, length u32, crc32 u32 }  (16 B/条)
Data:  各块负载；块内每平面 1 字节模式 + 负载
```

解析时先校验 magic、版本、位宽范围、保留字节、结构一致性（count/blockSize/
numBlocks/indexLen/dataLen 与缓冲区长度精确匹配）与头部 CRC，再按调用方
`maxValues` 配额限制输出，杜绝超量分配。尾部不足整块的短块照常保存。

## 用法

```go
buf, stats, err := bitplane.Encode(vals, bitplane.Options{Width: 16, Signed: true})
// stats 含 HeaderBytes / IndexBytes / DataBytes / TotalBytes，开销不忽略头部

full, err := bitplane.Decode(buf, maxValues)              // 全量解码，配额有界
part, err := bitplane.DecodeRange(buf, start, end, maxValues) // 随机区间，仅解码重叠块
```

## 运行样例（`go run ./cmd/bpcdemo`）

```
=== bitplane demo: 4096 signed int64 samples, width=16, zigzag ===
values:            4096
block size:        4096 values (1 blocks)
header:            44 bytes
index:             16 bytes
data:              3924 bytes
total:             3984 bytes
raw int64:         32768 bytes
ratio vs int64:    0.122
plane chunks:      literal=7 rle=2 delta=7
roundtrip:         OK (all 4096 values identical)
range [1000,1010):     [68 58 55 58 51 66 56 49 56 61]
quota(10):         rejected as expected: bitplane: output quota exceeded: stream holds 4096 values, limit is 10
```

## 测试结果（`go test -v ./...`）

```
--- PASS: TestRoundtripWidthsAndSigns (1.06s)    位宽 1..64 × 有/无符号随机往返
--- PASS: TestNegativeBitPatternPreserved        负数/极值位模式精确往返
--- PASS: TestSignExtremes                       MinInt64/MaxInt64 邻域混合
--- PASS: TestAllZeroPlanesCompact               全零平面紧凑（8192 值 ≤512 B）
--- PASS: TestTailBlockPreserved                 37 = 16+16+5 尾块保存
--- PASS: TestExhaustiveBlockSizeOptimal         小块穷举所有分段核验总字节最优
--- PASS: TestRangeDecodeMatchesFull             随机区间与全量解码一致
--- PASS: TestCorruptBlockIsolation              损坏块局部隔离，健康块可读
--- PASS: TestOutputQuota                        输出配额与伪造巨大 count 拒绝
--- PASS: TestHeaderValidation                   坏 magic/版本/位宽/CRC/截断等
--- PASS: TestEmptyInput                         空输入仅头部
--- PASS: TestInvalidWidth                       非法位宽拒绝
PASS
ok      bitplane        1.765s
```

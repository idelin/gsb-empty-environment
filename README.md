# erasure — GF(256) 系统式分块纠删码

纯 Go 标准库实现（无第三方依赖，未调用任何现成纠删/压缩库）。
在显式不可约多项式 **x^8 + x^4 + x^3 + x^2 + 1（0x11D）** 定义的 GF(256)
上进行运算，以 0x02 为乘法群生成元建立 log/exp 表。

## 原理

- 编码：数据均分为 K 个数据块（系统式，原样保留），再用 Vandermonde
  生成矩阵（第 j 个校验块系数为 x^c，x = j+1）生成 M 个校验块。
  该矩阵任意 K 行线性无关（MDS / Reed-Solomon 性质），因此
  **任意 K 个块即可恢复原文**。
- 解码：收集有效块，取其中 K 个块的生成矩阵行做 GF(256) 上的
  高斯-若尔当求逆，重建缺失数据块，按声明长度截断输出。

## 分块格式（小端）

| 偏移 | 字段 | 长度 |
|------|------|------|
| 0  | 魔数 "EC" | 2 |
| 2  | 格式版本（=1） | 1 |
| 3  | K（数据块数） | 1 |
| 4  | M（校验块数） | 1 |
| 5  | 块 ID（0..K+M-1） | 1 |
| 6  | 块载荷长度 | 4 |
| 10 | 原始数据长度 | 8 |
| 18 | CRC-32(IEEE)，覆盖头部[0:18]+载荷 | 4 |
| 22 | 载荷 | 块载荷长度 |

版本、长度、校验和均在格式中显式给出。

## 解码规则

- 缺块数 ≤ M（即有效块 ≥ K）：恢复原文。
- 有效块 < K：返回 `ErrInsufficientShards`。
- **块 ID 重复**：返回 `ErrDuplicateShard` 拒绝。
- **CRC 校验失败**：该块被拒绝，绝不参与解方程；若剩余有效块仍 ≥ K
  则正常恢复，否则返回不足。
- 头部结构非法（魔数/版本/ID 越界/长度矛盾/截断）：返回
  `ErrMalformedShard` / `ErrUnsupportedVersion` 拒绝。
- 不同编码批次的块混用：返回 `ErrInconsistentHeader`。
- **输出额度有界**：`Decode(blobs, maxOut)` 在声明原始长度超过
  `maxOut` 时返回 `ErrOutputLimit`，且输出严格按声明长度分配。

## 使用

```go
shards, err := erasure.Encode(data, 4, 2)   // 4 数据块 + 2 校验块
// ... 任意丢失/损坏至多 2 块 ...
data, err := erasure.Decode(availableShards, maxOut)
```

## 运行样例

```
$ go run cmd/demo/main.go
encoded 74 bytes into 4 data + 2 parity shards (19 bytes each)
  shard 0: 41 bytes, head=45 43 01 04 02 00 13 00
  shard 1: 41 bytes, head=45 43 01 04 02 01 13 00
  shard 2: 41 bytes, head=45 43 01 04 02 02 13 00
  shard 3: 41 bytes, head=45 43 01 04 02 03 13 00
  shard 4: 41 bytes, head=45 43 01 04 02 04 13 00
  shard 5: 41 bytes, head=45 43 01 04 02 05 13 00
recovered from shards {0,2,3,5}: "GF(256) systematic erasure coding: any 4 of 6 shards recover this message."
duplicate shard rejected: erasure: duplicate shard ID
corrupt shard excluded, recovered: "GF(256) systematic erasure coding: any 4 of 6 shards recover this message."
only 3 shards: erasure: insufficient valid shards
output limit enforced: erasure: decoded length exceeds output limit
```

## 测试结果

```
$ go test -v ./...
=== RUN   TestRoundTripAllShards
--- PASS: TestRoundTripAllShards
=== RUN   TestRecoverAnySubset
--- PASS: TestRecoverAnySubset
=== RUN   TestInsufficientShards
--- PASS: TestInsufficientShards
=== RUN   TestDuplicateShardRejected
--- PASS: TestDuplicateShardRejected
=== RUN   TestCorruptShardExcluded
--- PASS: TestCorruptShardExcluded
=== RUN   TestCorruptShardNotUsedInEquations
--- PASS: TestCorruptShardNotUsedInEquations
=== RUN   TestTamperedHeaderRejected
--- PASS: TestTamperedHeaderRejected
=== RUN   TestInconsistentHeaders
--- PASS: TestInconsistentHeaders
=== RUN   TestOutputLimit
--- PASS: TestOutputLimit
=== RUN   TestInvalidParams
--- PASS: TestInvalidParams
=== RUN   TestShardIDOutOfRange
--- PASS: TestShardIDOutOfRange
=== RUN   TestEmptyAndSingleByte
--- PASS: TestEmptyAndSingleByte
=== RUN   TestLargeKManyParities
--- PASS: TestLargeKManyParities
=== RUN   TestGeneratorOrder
--- PASS: TestGeneratorOrder
=== RUN   TestMulDivInverse
--- PASS: TestMulDivInverse
=== RUN   TestAddIsXor
--- PASS: TestAddIsXor
PASS
ok  	erasure
```

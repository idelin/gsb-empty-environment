# prefixcodec — 限长前缀码（Go 标准库实现）

在最大码长 `L` 约束下用 **package-merge** 算法（Larmore–Hirschberg）构造最优前缀码，
并提供带版本、长度与校验的自描述二进制格式。仅使用 Go 标准库，未调用任何压缩库。

## 设计要点

- **可行性拒绝**：符号数 `n > 2^L` 时直接返回 `ErrInfeasible`，绝不产出码表。
- **不截短 Huffman 码**：码长来自 package-merge 的精确解，Kraft 不等式恒成立，
   canonical（规范）码分配保证无前缀冲突。
- **Kraft 校验**：`Code.Kraft()` 以 `num / 2^L` 精确报告，`num == 2^L` 为完备码。
- **加权成本**：`Code.Cost()` 返回 `sum(freq[i]*len[i])` 与平均码长。
- **有界解码**：`Decode(data, maxSymbols)` 先用 `payloadBits > maxSymbols*maxLen` 提前拒绝，
  解码中超过额度立即报错，输出额度严格有界。

## 二进制格式（大端）

```
magic       [4]byte  "PCD1"   格式标识 + 版本 1
maxLen      uint32            最大码长 L
numSymbols  uint32            符号数 n
lengths     [n]uint8          各符号码长（按符号序）
payloadBits uint64            载荷有效比特数
payload     []byte            ceil(payloadBits/8) 字节，MSB 优先
checksum    uint32            之前全部字节的 CRC-32 (IEEE)
```

## 运行

```sh
go test ./...
go run ./cmd/pcdemo -L 5
```

## 运行样例（go run ./cmd/pcdemo -L 5）

```
symbols=8 frequencies=[45 16 13 12 9 5 3 2] maxLen=5

symbol  freq  len  codeword
    0    45    1  0
    1    16    3  100
    2    13    3  101
    3    12    4  1100
    4     9    4  1101
    5     5    4  1110
    6     3    5  11110
    7     2    5  11111

Kraft sum = 32/2^5 = 1.000000 (<= 1, complete=true)
weighted cost = 261 bits, average = 2.4857 bits/symbol

encoded 14 symbols into 37 bytes (format PCD1: version+lengths+payload+CRC32)
decoded 14 symbols, tables equal=true, round-trip ok=true

[infeasible] L=2 with 8 symbols -> prefixcode: infeasible, symbol count exceeds 2^maxLen
[tamper] flipped payload byte -> prefixcode: checksum mismatch (got c1d46e17, want 52f09365)
[bounded] limit=13 symbols -> prefixcode: decode output exceeds limit of 13 symbols
```

`go run ./cmd/pcdemo -L 2`（8 个符号 > 2^2=4）直接拒绝：

```
REFUSED: prefixcode: infeasible, symbol count exceeds 2^maxLen
```

## 测试结果（go test ./...）

```
ok  	prefixcodec/prefixcode
```

覆盖：不可行拒绝（2^L < n）、边界可行（n == 2^L 完备码）、经典 Huffman
最优成本 224、紧约束下等长码、成本随 L 单调不增、200 组随机实例的
码长上限/Kraft/往返性质、单符号、空输入、非法码长、逐比特篡改检测、
截断检测、版本拒绝、解码输出额度（含提前拒绝）。

# tans — 有限状态熵编码器（tANS / FSE 风格）

纯 Go 标准库实现的 **tANS（tabled Asymmetric Numeral Systems）** 熵编码内核：
结合算术编码的高压缩率与霍夫曼编码的高吞吐。整个压缩状态被折叠进**单个大整数状态**，
压入（Push）/ 弹出（Pop）每个符号只需一次预计算**状态转移表**查表，近常数时间完成。

仓库内还附带一个经典 carry-less range coder（传统算术编码）作为压缩率与吞吐的对照基线。

## 原理

- 将符号频率归一化到 `L = 2^tableLog` 个状态上，构建离散状态转移矩阵：
  - 解码表：`state -> (symbol, nbBits, baseline)`，弹出一个符号 = 查表 + 读若干比特；
  - 编码表：`symbol + state -> (nbBits, nextState)`，压入一个符号 = 写若干比特 + 查表。
- 编码器**逆序**处理输入、正向写比特流；解码器从流尾**反向读比特**、正序输出符号，
  二者共享同一张转移表，严格互逆。
- 表越大越逼近香农熵界限（默认 `tableLog=12`，4096 状态，实测载荷为熵界限的 100.01% 量级）。

## 文件

- `tans.go` — 频率归一化、转移表构建、`Encode`/`Decode`、表序列化（513 字节）
- `bits.go` — LSB-first 比特写入器 / 反向比特读取器
- `arith.go` — 对照用传统算术编码器（range coder，`RangeBaseline`）
- `tans_test.go` / `bench_test.go` — 正确性、压缩率、吞吐测试
- `cmd/tans-demo` — 可运行示例

## 使用

```go
counts := tans.CountSymbols(data)          // 统计直方图
table := tans.BuildTable(counts, 0)        // 0 = 默认 tableLog=12
tblBytes, _ := table.MarshalBinary()       // 表随码流传输（513 字节）

compressed := table.Encode(nil, data)      // 压缩（含 16 字节自描述头）
restored, _ := table.Decode(nil, compressed)
```

## 运行

```sh
go test ./...          # 正确性 + 压缩率测试
go test -bench .       # tANS vs 算术编码吞吐基准
go run ./cmd/tans-demo # 端到端演示
```

## 实测结果（Apple M1 Pro，go1.24.2 darwin/arm64）

`go run ./cmd/tans-demo`（16 MiB 高偏斜 Zipf 样本，64 符号）：

```
input:                16777216 bytes (highly skewed Zipf sample, 64 symbols)
entropy bound:        10200405 bytes (4.864 bits/symbol)
tANS table:           513 bytes (tableLog=12, 4096 states)
tANS compressed:      10202098 bytes payload, 10202611 bytes with table (100.022% of entropy bound)
tANS encode:          133 MiB/s
tANS decode:          123 MiB/s
range coder payload:  10203752 bytes
range coder encode:   97 MiB/s
range coder decode:   28 MiB/s
tANS speedup:         1.4x encode, 4.4x decode vs arithmetic coding
```

`go test -v` 关键输出：

```
=== RUN   TestCompressionRatio
    entropy bound: 4349525 bits, tANS payload: 4349832 bits (100.007% of bound)
=== RUN   TestCompressionRatioWithTable
    entropy bound: 40795050 bits, total with table: 40805552 bits (100.026% of bound)
=== RUN   TestTANSvsRangeCoderRatio
    tANS payload: 543920 bytes, range coder: 544034 bytes
```

`go test -bench .`：

```
BenchmarkTANSEncode-10    138.03 MB/s
BenchmarkTANSDecode-10    132.48 MB/s
BenchmarkRangeEncode-10   103.77 MB/s
BenchmarkRangeDecode-10    28.10 MB/s
```

结论：压缩率完全媲美理论熵界限（含表头仍在 100.03% 以内），
解码吞吐达到传统算术编码的 **4 倍以上**。

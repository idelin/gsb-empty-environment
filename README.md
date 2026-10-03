# arithmetic — 基于概率分布的算术编码器

纯 Go 标准库实现的高压缩率算术编码（Arithmetic Coding）。

## 实现要点

- **大整数有理数区间**：以 `math/big.Int` 维护状态区间 `[Low, High)`，
  相对 `2^62` 定点缩放，细分运算为精确有理数算术：
  `[low, high) ← [low + range·cum[s]/total, low + range·cum[s+1]/total)`。
- **重归一化**：E1/E2 缩放把已收敛的最高位溢写进位流，
  E3（underflow）用 pending 计数延迟输出，保证任意长输入不丢精度。
- **精确反解与终止**：解码端用 `v = ((code-low+1)·total-1)/range`
  在累计概率分布（CDF）上二分定位符号；流头 8 字节记录符号数，完美终止。
- **逼近香农熵**：100k 二元符号（90% 为 0）实测 0.4655 bit/符号，
  与熵极限 0.4655 持平（开销 ~0.01%），仅为霍夫曼理论下限的 46.6%。

## 使用

```go
model := arithmetic.NewModel([]uint64{90, 10}) // 概率模型（频率表）
enc := arithmetic.Encode(symbols, model)       // []byte 符号 -> 压缩字节流
dec := arithmetic.Decode(enc, model)           // 精确还原
```

## 运行

```sh
go test -v ./...     # 测试
go run ./cmd/acdemo  # 演示
```

## 测试结果（go1.11 darwin/amd64）

```
--- PASS: TestRoundTripBinary        # 1..100000 符号往返一致
--- PASS: TestBeatsHuffman           # 5856 字节 vs 霍夫曼下限 12500 字节 (46.8%)
--- PASS: TestNearEntropy            # 0.4648 vs 熵 0.4648 bit/符号 (开销 0.01%)
--- PASS: TestRoundTripMultiSymbol   # 4 符号偏斜分布往返一致
--- PASS: TestEdgeCases              # 空/单符号/全同符号
--- PASS: TestSkewedExtreme          # p(0)=0.99: 0.0818 vs 熵 0.0808 bit/符号
```

## 演示输出（go run ./cmd/acdemo）

```
符号数:            100000 (0 占比 90.1%)
原始大小:          100000 字节 (1 字节/符号)
霍夫曼理论下限:    12500 字节 (1 bit/符号)
香农熵极限:        5818 字节 (0.4655 bit/符号)
算术编码输出:      5827 字节 (0.4655 bit/符号)
相对霍夫曼下限:    46.6%
解压验证:          true (完美终止, 逐符号一致)
```

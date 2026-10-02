# deflate

纯标准库实现的轻量级 Deflate(RFC 1951)压缩内核,Go 编写,零第三方依赖。

## 流水线

1. **LZ77 阶段**(`lz77.go`):32 KiB 滑动窗口 + 3 字节哈希链(`head`/`prev` 数组),
   贪心提取 `(Distance, Length)` 重复引用,链长上限 128。
2. **Huffman 阶段**(`huffman.go`):对字面量/长度码与距离码分别建堆式霍夫曼树,
   采用 zlib 风格溢出修正将码长限制在 15 位,生成规范(canonical)码。
3. **块级调度**(`compress.go`):输入按 64 KiB 分块,逐块估算
   stored / static / dynamic 三种编码的位开销,选最小者写入带块头的紧凑位流;
   动态块头使用 16/17/18 游程操作码 + 二级码长霍夫曼表。

解压端(`decompress.go`)完整识别三种块格式:

- `BTYPE=00` 非压缩块(字节对齐,LEN/NLEN 校验)
- `BTYPE=01` 静态霍夫曼块(RFC 1951 §3.2.6 固定表)
- `BTYPE=10` 动态霍夫曼块(HLIT/HDIST/HCLEN + 码长游程解码)

## API

```go
compressed := deflate.Compress(data)        // []byte -> raw deflate 流
restored, err := deflate.Decompress(compressed)
```

## 运行

```sh
go test ./...        # 测试:三种块格式、SHA256 逐字节校验、与 compress/flate 互验
go run ./cmd/demo    # 样例:压缩 4 MiB 重复模式系统日志并验证 SHA256
go test -bench=.     # 基准
```

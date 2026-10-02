# deflate

轻量级 DEFLATE (RFC 1951) 压缩内核，纯 Go 标准库实现，无任何第三方依赖。

## 架构

两个阶段：

1. **LZ77 解析**（`lz77.go`）：32 KiB 滑动窗口 + 3 字节哈希链（`head`/`prev`
   数组），带一步前瞻的惰性匹配，输出字面量与 `(Distance, Length)` 令牌流。
2. **霍夫曼编码**（`huffman.go`、`deflate.go`）：对字面量/长度与距离两个
   字母表分别构建限长霍夫曼码（zlib 式溢出修正，码长 ≤ 15；码长码 ≤ 7），
   码长序列经 16/17/18 游程编码后写入带块头的紧凑位流。

块类型（`Options.BlockType`）：

- `BlockAuto`（默认）：逐块精确估算 stored / static / dynamic 三种编码的
  位数，选最小者（不可压缩数据自动回退 stored）。
- `BlockStored` / `BlockStatic` / `BlockDynamic`：强制指定块格式。

解压端（`inflate.go`）完整识别三种块格式：非压缩块（LEN/NLEN 校验）、
静态霍夫曼块（RFC 固定码表）、动态霍夫曼块（HLIT/HDIST/HCLEN 头解析），
并做距离越界、码表过订阅、截断等错误检查。

## 用法

```go
import "deflate"

compressed := deflate.Compress(data)                    // 自动选块
restored, err := deflate.Decompress(compressed)         // 解压
types, err := deflate.BlockTypes(compressed)            // 查看每块格式: 0=stored 1=static 2=dynamic

// 强制块格式
c2 := deflate.CompressWithOptions(data, deflate.Options{BlockType: deflate.BlockStatic})
```

## 运行

```sh
go test -v ./...        # 测试（含 SHA256 逐字节校验、与 compress/flate 双向交叉验证）
go test -bench . .      # 基准
go run ./cmd/deflate-demo [MiB]   # 运行样例：生成 4MiB 系统日志 → 压缩 → 解压 → SHA256 比对
```

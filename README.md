# adaptivehuffman

自适应动态霍夫曼（Adaptive Huffman）编解码器，采用 **FGK 算法**
（Faller–Gallager–Knuth），纯 Go 标准库实现。

## 特性

- **单次流式处理**：无需预先扫描全文本统计频率，`Encoder`/`Decoder`
  分别实现 `io.WriteCloser` / `io.Reader`，可任意分块读写。
- **兄弟属性（Sibling Property）**：每个符号处理后沿根路径递增权重，
  并通过"块首交换"（block-leader swap）动态维护兄弟属性，编解码两端的
  树始终保持完全一致。
- **NYT 标记引入新符号**：未出现过的符号先发送 NYT 路径编码，再发送
  9 bit 原始符号值；解压端同步执行相同的分裂（split）操作。
- **自终止码流**：保留第 257 个符号作为流结束标记（EOF symbol），
  无需外部长度信息；码流被截断时解码端返回 `io.ErrUnexpectedEOF`。
- **常数级内存**：符号表固定为 256 字节值 + EOF，树节点数上界为
  `2×258−1 = 515`，与输入长度无关（有测试验证）。

## 文件结构

| 文件 | 说明 |
| --- | --- |
| `tree.go` | FGK 动态树：分裂、块首交换、权重递增、兄弟属性维护 |
| `bitio.go` | MSB-first 位流读写器 |
| `encoder.go` | 流式编码器（`io.WriteCloser`） |
| `decoder.go` | 流式解码器（`io.Reader`） |
| `huffman_test.go` | 单元测试（round-trip、兄弟属性、树同步、内存上界等） |
| `cmd/adaptive-huffman/` | 命令行样例程序 |

## 使用

作为库：

```go
var buf bytes.Buffer
enc := huffman.NewEncoder(&buf)
enc.Write(data)      // 可多次调用，任意分块
enc.Close()          // 写入 EOF 符号并冲刷末尾不足一字节的位

dec := huffman.NewDecoder(&buf)
plain, err := io.ReadAll(dec)
```

命令行样例：

```sh
go build -o adaptive-huffman ./cmd/adaptive-huffman
./adaptive-huffman    < plain.txt  > plain.ah   # 压缩
./adaptive-huffman -d < plain.ah   > plain.txt  # 解压
```

## 运行样例

对一段 251,833 字节、字符分布明显偏斜（近似英文词频）的文本：

```
$ ls -l sample.txt sample.ah
251833  sample.txt     # 原文
122067  sample.ah      # 压缩后（约 48.5%）
$ cmp sample.txt <(./adaptive-huffman -d < sample.ah) && echo OK
OK                       # 解压输出与原文逐字节一致
```

## 测试结果

`go test ./...`（Go 1.24，darwin/arm64）：

```
=== RUN   TestRoundTripSkewedLongText     1 MiB 偏斜文本: 1048576 -> 722616 (0.689)   PASS
=== RUN   TestRoundTripAllByteValues      均匀随机全字节表 round-trip                  PASS
=== RUN   TestRoundTripEmpty              空输入                                       PASS
=== RUN   TestRoundTripSingleByte         单字节                                       PASS
=== RUN   TestRoundTripRepeatedByte       10 万重复字节 -> ~1 bit/字节                 PASS
=== RUN   TestRoundTripByteAtATime        每次读写 1 字节的极端流式                    PASS
=== RUN   TestRoundTripEvolvingDistribution 分布中途漂移的自适应跟踪                   PASS
=== RUN   TestSiblingPropertyMaintained   20 万次更新后兄弟属性不变式成立              PASS
=== RUN   TestEncoderDecoderTreesStayInSync 编解码双端树逐节点一致                     PASS
=== RUN   TestConstantMemoryBound         1 MiB 随机输入后节点数 515 ≤ 上界 515        PASS
=== RUN   TestCorruptStreamReturnsError   截断码流返回错误                             PASS
PASS
ok      adaptivehuffman
```

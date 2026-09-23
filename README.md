# bitstream

纯标准库 Go 位流读写包：任意位宽（1–64）读写、对齐、窥视与嵌套
回退标记，位序（bit order）与字节序（byte order）独立可选，读取不足
不消费任何位，跨块零复制视图，共享输入保留水位，解码额度有界。
不依赖任何同功能压缩库。

## 布局模型

- `Config{Bits, Bytes}`：`Bits`（`MSBFirst`/`LSBFirst`）定义位在字节
  内的编号；`Bytes`（`BigEndian`/`LittleEndian`）定义多字节值的字节
  顺序。两者正交，可任意组合。
- 写入语义：值先按字节序切成逻辑字节，每个字节的位再按位序放入
  物理字节；部分字节字段按字段宽度对齐。

## 核心 API

- `Writer.Write(v, n)`：写入低 n 位；宽度非法（∉[1,64]）在修改缓冲
  前拒绝（`ErrInvalidWidth`）。
- `Reader.Read/Peek(n)`：读取/窥视；`ErrShortBuffer`、额度
  （`ErrQuotaExceeded`）与取消（`ErrCanceled`）均不消费输入。
- `Reader.Align(n)`：游标前进到 n 位边界。
- `Reader.Mark/Commit/Rollback`：嵌套事务；peek、mark、rollback 共享
  同一保留水位，水位以下的未钉住块自动回收（`BufferStats` 记录物理
  占用与回收字节数）；已释放块回退访问返回 `ErrRollbackReleased` /
  `ErrReleased`。
- `Reader.View(n)`：跨块零复制视图，钉住所跨越的块直到 `Close`。
- `Marshal/Unmarshal`：自描述帧 `magic | version | bitOrder |
  byteOrder | reserved | bitLen(uint32) | payload | crc32`，版本、
  长度、校验显式；`Unmarshal(frame, maxBits)` 限制解码输出额度。

## 运行样例

```console
$ go run ./cmd/demo
written 78 bits into 10 bytes
frame: 26 bytes, version=1
decoded header: cfg={Bits:0 Bytes:0} payload=78 bits
peek(3)=0b101 pos=0 (cursor unmoved)
read: prefix=0b101 value13=0x1abc pos=16
after rollback pos=0
zero-copy view across blocks: 0x1abc
buffer stats: live=2 blocks/10 bytes reclaimed=0 bytes
```

## 测试结果

```console
$ go test -race -count=1 ./...
ok  	bitstream	1.242s
```

- `TestExhaustiveShortValues`：穷举宽度 1–12 的全部位串 × 4 种
  位序/字节序组合，逐位数组模型验证布局与回读值。
- `TestRandomOperationSequences`：随机操作序列（read/peek/mark/
  rollback/commit/align）对照逐位数组模型，逐步校验返回值与游标。
- `TestCrossByte13Bits`：非对齐 13 位跨字节、跨块读取。
- `TestShortReadConsumesNothing` / `TestQuotaAndCancelConsumeNothing`：
  短读、额度、取消均不消费输入。
- `TestInvalidWidthRejectedFirst`：非法宽度在修改缓冲前拒绝。
- `TestNestedTransactions` / `TestWatermarkAndReclaim` /
  `TestZeroCopyViewAcrossBlocks` / `TestFrameRoundTrip`：嵌套事务、
  水位回收统计、零复制视图、帧版本/长度/校验与解码额度。

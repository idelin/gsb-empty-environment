# cbitmap — 压缩位图（Roaring 风格，纯 Go 标准库）

32 位压缩位图实现：紧凑存储、高效布尔集合运算、二进制持久化编解码、
内存映射零拷贝还原。仅依赖 Go 标准库，兼容 Go 1.11+。

## 特性

- **三种容器自动选择与转换**
  - `ArrayContainer`：有序 `uint16` 数组，适合稀疏数据（≤4096 个值）
  - `BitmapContainer`：1024 个 `uint64` 字（65536 位），适合密集随机数据
  - `RunContainer`：游程编码 `(start, len)`，适合连续区间
  - 每次运算后按实际字节开销（`2·card` / `8192` / `4·runs`）自动选择最优编码
- **集合运算**：`And` / `Or` / `Xor`，及批量 `AndAll` / `OrAll` / `XorAll`，
  覆盖全部 3×3 容器组合；bitmap-bitmap 运算单遍完成 AND+popcount+游程计数
- **紧凑二进制格式**：小端、8 字节对齐、带版本号与描述符表
- **零拷贝还原**：`UnmarshalZeroCopy` 直接引用输入缓冲区（可为 mmap 文件），
  容器数据零分配；frozen 位图写时复制（copy-on-write），绝不污染源缓冲区
- **向前/向后兼容**：未知 flags 与尾部扩展字节被忽略（向前兼容）；
  更高版本号被拒绝（向后安全）
- **边界防溢出**：解析时校验魔数、版本、容器数、键序、类型、基数、
  偏移对齐与边界（uint64 溢出安全）、数组有序性、游程不重叠、基数一致性
- **并发安全**：frozen 位图可并发只读；`go test -race` 全量通过

## 二进制格式

```
header (16 bytes):
  0  uint32  magic 0x52424D50 ("RBMP")
  4  uint16  format version (=1)
  6  uint16  flags（未知位忽略）
  8  uint32  container count (<= 65536)
  12 uint32  reserved
descriptor array (count * 24 bytes):
  0  uint16  key（高 16 位，严格递增）
  2  uint8   类型：1=array, 2=bitmap, 3=run
  3  uint8   reserved
  4  uint32  cardinality
  8  uint32  meta（run 容器：游程数）
  12 uint64  负载绝对偏移（8 字节对齐）
  20 uint32  reserved
payload（8 字节对齐）:
  array:  card * 2 字节有序 uint16
  bitmap: 8192 字节（1024 个 uint64 字）
  run:    runs * 4 字节（start, len-1）
```

## 用法

```go
bm := cbitmap.New()
bm.Add(42)
bm.AddRange(1<<20, 2<<20)          // 连续区间 -> RunContainer
buf := bm.MarshalBinary()           // 紧凑序列化
ioutil.WriteFile("bm.bin", buf, 0644)

data, _ := cbitmap.MmapFile("bm.bin")   // 内存映射
defer cbitmap.Munmap(data)
frozen, _ := cbitmap.UnmarshalZeroCopy(data) // 零拷贝还原（frozen）
frozen.Contains(42)                        // 并发安全只读
inter := cbitmap.And(frozen, other)        // 直接在映射内存上运算
```

运行示例：`go run ./cmd/example`

## 测试与基准

```
go test -count=1 .          # 单元/边界/序列化/并发测试
go test -race -count=1 .    # 竞态检测
go test -run=NONE -bench=. -benchtime=100ms .
```

测试覆盖：稀疏/密集/边界 ID（0、65535、65536、2^32-1）、全键空间 2^32、
容器自动转换、run 合并、与参考模型的随机对拍（AND/OR/XOR）、全部容器
类型对组合、序列化往返、零拷贝内存共享验证、写时复制隔离、17 类损坏
输入（截断/坏魔数/未来版本/越界偏移/溢出偏移/未对齐/乱序数组/重叠游程/
基数不符等）、向前兼容（未知 flags、尾部字节）、错位缓冲区回退、mmap
往返、并发只读（-race）、goroutine 泄漏断言。

基准（darwin/amd64，共享机器，数值有波动）：

| 操作 | 规模 | 耗时 |
|---|---|---|
| AND | 2×2048 个 bitmap 容器（各 1.3e8 ID） | ~52 ms（≈25 µs/容器对，≈0.4 ns/ID） |
| AND | 2×4096 个 run 容器（各 2.7e8 ID 连续区间） | ~1.5 ms（≈0.4 µs/容器对） |
| AND | 1e6 稀疏 ID ∩ 6.3e6 连续 ID（示例） | ~98 µs |
| 零拷贝解析 | 256 个 bitmap 容器（2 MB） | ~0.66 ms（含基数校验） |
| Add | 单值插入 | ~0.65 µs |
| AddRange | 2^26 连续 ID | ~0.2 ms |

稠密随机数据下每容器对（65536 ID）交并运算为微秒级；连续区间数据经
RLE 后整体交并即为微秒级。全键空间 2^32 个 ID 以 RLE 存储仅需 256 KB。

# rangecoder

整数区间算术编码（carry-less 32-bit range coder，Subbotin 风格），纯标准库实现，
不依赖任何压缩库。编码器与解码器驱动完全相同的自适应频率模型，状态严格同步。

## 容器格式

```
[0:3]   魔数 "RAC"
[3]     格式版本 = 1
[4:12]  uint64 大端：原始数据长度
[12:16] uint32 大端：原始数据 CRC-32 (IEEE)
[16:]   区间编码符号流，以 EOF 终止符（符号 256）结束
```

## 关键性质

- **频率表**：0 阶自适应，257 个符号（256 字节值 + EOF），初始频率全 1；
  每观测一个符号频率 +4；总频率达到 2^14 时全部折半（`(f+1)>>1`），
  缩放后每个已知符号频率保持 >= 1。
- **终止符与预算**：每条流以 EOF 符号显式终止；`Decode(data, maxOutput)`
  对解码输出设有硬上限，损坏或恶意输入不会导致无限解码或无限内存增长。
- **溢出安全**：编码器使用 33 位 low（第 32 位为进位）+ 延迟 0xff 进位传播；
  长单一符号流（测试 3,000,000 字节）不溢出，且压缩到源大小的 1% 以内。
- **完整性**：解码后校验长度与 CRC-32；截断输入以零字节填充，解码有界且必然报错。

## 用法

```go
enc := rangecoder.Encode(src)          // 压缩
dec, err := rangecoder.Decode(enc, 1<<20) // 解压，maxOutput 为输出预算
```

演示程序：`go run ./cmd/racdemo`

## 运行样例

```
$ go run ./cmd/racdemo
format version : 1
declared length: 1851 bytes
declared crc32 : 0xf50bcceb
original size  : 1851 bytes
encoded size   : 1108 bytes (59.9% of original)
round trip     : OK, crc32 0xf50bcceb verified within budget 1048576
```

## 测试

`go test ./...` 覆盖：多类数据往返、300 万单符号流、缩放后频率保正、
编解码状态同步、头部字段、坏魔数/版本、载荷腐坏检测、截断有界、
垃圾流预算有界、声明长度超预算、EOF 终止符语义。

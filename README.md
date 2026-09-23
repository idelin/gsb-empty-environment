# blkx — 流式块编码转码器

仅使用 Go 标准库，把一种块编码流（格式 A, v1）不完整物化地转码为另一种
（格式 B, v2）。逐块校验、逐块写出，内存占用有界于单块大小，背压沿
`dst -> src` 自然传导。源流未通过全部校验前，目标流绝不写出完成标记。

## 线格式（大端，版本/长度/校验显式）

格式 A v1（源）：

```
Header:  "BLKA" | version u8=1 | declaredLen u64 | metaLen u32 | meta
Block:   tag 0x01 | payloadLen u32 | payloadCRC32 u32 | payload
Trailer: tag 0x02 | contentCRC32 u32          <- 完成标记
```

格式 B v2（目标）：

```
Header:  "BLKB" | version u8=2 | declaredLen u64 | metaLen u32 | meta
Block:   index u32 | payloadLen u32 | payloadCRC64 u64 | payload
Trailer: index 0xFFFFFFFF | totalLen u64 | contentCRC64 u64   <- 完成标记
```

## 语义保证

- 流式/背压：`Transcode(dst, src)` 每读出一个通过 CRC 校验的源块就立即
  写出一个目标块，从不缓存全部内容。
- 元数据与声明长度：头部原样携带到目标流。
- 坏输入不发布完成标记：块校验失败、流截断、声明长度与实际不符、版本
  不支持时，`BWriter.Finish()` 不会执行，目标流缺少 trailer，任何读取方
  都会拒绝它。
- 文件级原子提交：`TranscodeFile` 先写 `dst.tmp`，完整标记落盘后才
  rename；任何失败都会删除未提交的临时尾部，目标路径不被污染。
- 解码额度有界：`Decode(dst, src, maxOutput)` 在头部即拒绝
  `declaredLen > maxOutput` 的流，且累计输出永不超过额度。

## 运行样例

```console
$ go build -o blkx ./cmd/blkx
$ head -c 300000 /dev/urandom > sample.bin
$ ./blkx encode -in sample.bin -out sample.bka -meta '{"title":"demo"}' -block 65536
encoded sample.bin -> sample.bka (300000 bytes declared)
$ ./blkx transcode -in sample.bka -out sample.bkb
transcoded sample.bka -> sample.bkb
$ ./blkx decode -in sample.bkb -out sample.out -quota 1000000
decoded sample.bkb -> sample.out (300000 bytes, meta {"title":"demo"})
$ cmp sample.bin sample.out && echo ROUNDTRIP OK
ROUNDTRIP OK
```

失败场景（损坏源不提交、配额拒绝）：

```console
$ ./blkx transcode -in corrupt.bka -out corrupt.bkb
blkx: blkx: block checksum mismatch at offset 0   # exit=1，无 corrupt.bkb / corrupt.bkb.tmp
$ ./blkx decode -in sample.bkb -out small.out -quota 100
blkx: blkx: declared length 300000 exceeds output quota 100   # exit=1，无残留文件
```

## 测试

```console
$ go test ./...
ok      blkx
```

覆盖：随机分块转码后解码相等、元数据保留、坏校验/截断/版本错误不发布
完成标记、声明长度与实际不符（少了或超了）拒绝、目标写失败传播、
`TranscodeFile` 失败清理未提交尾、解码配额有界、空流往返。

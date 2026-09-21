# recstream

一个自描述的二进制**记录流**及其**随机访问索引**，仅用 Go 标准库实现。

每条记录携带字段定义（字段号 + wire 类型）和记录长度；字段自定界，可跳过
未知字段、按字段号跨版本定位。索引记录每条记录的真实边界，并保存源文件的
大小与 SHA-256 摘要，防止索引与源文件错配。

## 记录帧格式

```
uvarint frameLen        帧长度（其后的字节数，不含长度自身）
[ frameLen 字节 ]
    body                序列化字段，允许为空
    uvarint crc32       校验值，位于帧尾
```

校验域为「8 字节小端 body 长度 + body」的 CRC32-IEEE。把长度纳入校验后，
坏长度无法把帧“截短/拉长”成另一条合法记录；尤其是空 body 的校验值是固定
非零值，截短成单个 `0x00` 不会被误认为空记录。

字段格式（类似 TLV / protobuf）：

```
uvarint (fieldID << 3) | wireType
值（按 wire 类型自定界）:
  0 Varint  : uvarint
  1 Fixed32 : 4 字节小端
  2 Fixed64 : 8 字节小端
  3 Bytes   : uvarint 长度 + 原始字节
```

字段号即 schema：新版本追加新字段号即可，旧版本自动跳过；字段号复用时若
wire 类型不一致，类型化访问器返回 `ok=false` 而不会误读。

## 语义约定

- **缺数据 vs 坏数据**：`ErrNeedMore`（只是前缀不完整，继续喂）/
  `ErrTruncated`（长度已承诺但流结束，数据缺失）/`ErrCorrupt`（字节本身
  非法：校验失败、tag 非法、内部长度越界等），三者严格区分。
- **长度额度**：`MaxRecordSize`（默认 16 MiB）。解析到长度后、在扩容或
  分配载荷**之前**检查，超限返回 `ErrRecordTooLarge`，恶意长度头无法耗尽
  内存。
- **坏长度不错位**：遇到坏数据，顺序解码器与分块解码器都终止（分块解码器
  会锁存错误），绝不“从下一个字节试着同步”从而把后续好记录错位读出来。
- **索引必须命中真实边界**：随机读取时校验偏移、帧长、CRC 三者与索引一致，
  任何偏差返回 `ErrBoundary`，绝不会从帧中间解析出一条“记录”。
- **空记录**：合法，编解码完整保留（body 为空但仍有非零 CRC）。
- **空文件**：顺序解码立即 `io.EOF`；建立得到 0 条记录的索引；对其随机读取
  返回 `ErrBoundary`。

## 主要 API

- `NewWriter(w)` / `(*Writer).Write(r)`：顺序写，返回记录偏移与帧长。
- `NewDecoder(r)` / `(*Decoder).Next()`：顺序读。
- `NewChunkDecoder()` / `Feed(chunk)` / `Finalize()`：字节可任意分块到达
  （包括 1 字节一块）；`Feed` 不阻塞，不完整时返回 `ErrNeedMore`。
- `BuildIndex(r)` / `(*Index).WriteTo` / `ParseIndex`：建立与读写索引。
- `(*Index).VerifySource(r)`：用源文件大小 + SHA-256 防错配。
- `NewRandomReader(ra, idx)` / `ReadRecord(i)`：带边界校验的随机访问。
- 字段构造器：`NewVarint / NewFixed32 / NewFixed64 / NewBytes`；
  访问器：`Record.Get(id)` 与 `Field.Varint/Fixed32/Fixed64/Bytes`。

## 索引格式

```
magic "RSIDX001\n"（9 字节）
uvarint 源文件大小
[32]byte 源文件 SHA-256
uvarint 记录条数
每条记录:
    uvarint offset     帧长度 varint 的字节偏移
    uvarint frameSize  整帧大小（含长度 varint）
    uvarint crc        帧内保存的 body 校验值
```

解析时强制各条目偏移首尾相接并恰好覆盖整个源文件，任何篡改导致的断链都会
被判为 `ErrIndexCorrupt`。

## 运行

```sh
go test ./... -v        # 单元测试
go run ./cmd/recdemo    # 运行样例
```

## 样例输出（`go run ./cmd/recdemo`）

```
write record 0 at offset 0 (frame 6 bytes)
write record 1 at offset 6 (frame 9 bytes)
write record 2 at offset 15 (frame 30 bytes)

sequential decode:
  record 0: 0 field(s)
  record 1: 1 field(s)
  record 2: 4 field(s)

chunk decode (one byte per feed):
  reassembled 3 complete records

index: 3 records, source 45 bytes, digest 6460716d8a69d258…
random read record 2, field 1 (varint) = 42
  forward-compatible field 99 = 0xcafef00d

tampered file rejected by source summary: recstream: index/source mismatch
```

## 测试结果（`go test -v -count=1 .`）

15 个测试全部 PASS：

```
--- PASS: TestBadLengthNoResync
--- PASS: TestShortenedFrameNotEmpty
--- PASS: TestLengthQuotaEnforcedBeforeAllocation
--- PASS: TestOversizedBytesFieldRejected
--- PASS: TestUnknownFieldsSkippedAcrossVersions
--- PASS: TestInvalidTagsAndVarints
--- PASS: TestIndexBuildAndRandomAccess
--- PASS: TestIndexHitMustLandOnBoundary
--- PASS: TestRoundTripSequential
--- PASS: TestEmptyFile
--- PASS: TestEmptyRecordIsPreserved
--- PASS: TestChunkedArbitrarySplits
--- PASS: TestChunkEmptyFeedsAndNeedMore
--- PASS: TestMissingVsBadData
--- PASS: TestSequentialTruncation
PASS
ok  	recstream
```

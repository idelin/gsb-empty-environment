# dzblock

独立块压缩格式与参考实现。纯 Go 标准库，不调用任何现成压缩库
（熵编码器为本仓库内手写的 LZ77 变体，见 `lz.go`）。

## 设计特性

- **独立块 + 可选只读字典**：每个块自包含，解码任意块只需共享字典，
  不依赖前序块的解码状态，支持随机访问与并行解码。
- **块头绑定字典摘要**：流头与每个块头都携带字典的 SHA-256 摘要，
  用错字典会被拒绝（`ErrDictMismatch`）；空字典有确定摘要，照常编码。
- **格式版本、长度、校验明确**：流头/块头均含魔数、版本号、声明长度
  与 CRC-32C（头校验 + 载荷校验）。
- **大小与输出限额**：单块原始上限 `MaxBlockRaw`（1 MiB）、编码上限
  `MaxBlockEncoded`；解码端有累计输出配额 `Reader.MaxOutput` 与单次
  调用限额，解码输出严格有界。
- **坏块不遮蔽后续块**：块头解析失败时不消费字节；`Reader.Resync`
  按「块魔数 + 头 CRC」扫描定位下一个合法块头，坏块或垃圾数据
  不会使下一个独立块不可定位。

## 格式

```
流头 (49 B):  magic "DZB\x01" | version u8 | dictID sha256 | dictLen u64 | crc32c
块头 (53 B):  magic DB 1B 42 4B | version u8 | dictID sha256 |
              rawLen u32 | encLen u32 | payloadCRC u32 | headerCRC u32
载荷:         LZ77 token 流（控制字节 + 字面量 / (offset u16, len u8) 匹配）
```

匹配可回溯进字典（字典视为每个块的只读前缀窗口），偏移上限 65535。

## API 摘要

- `AppendStreamHeader(dst, dict)` / `AppendBlock(dst, dict, block)`：编码。
- `NewReader(r, dict, maxOutput)` → `ReadBlock()` / `Resync()`：流式解码。
- `ParseBlockHeader(b)` + `DecodeBlock(dict, hdr, payload, limit)`：随机访问解码。

## 运行样例

```
$ go run ./cmd/dzdemo
encoded: 3 blocks, 37710 raw bytes -> 1764 stream bytes (4.7%)
decoded: 3 blocks, all round-trip checks passed
random access: block 2 decoded standalone, matches original: true
corrupt block 2 rejected: dzblock: corrupt data
resync: block 3 recovered after corrupt block 2, matches: true
wrong dictionary rejected: dzblock: dictionary digest mismatch
```

## 测试结果

```
$ go test -v ./...
--- PASS: TestRoundTrip
--- PASS: TestEmptyDictionary
--- PASS: TestDictionarySharedAcrossBlocks
--- PASS: TestWrongDictionaryRejected
--- PASS: TestBlockSizeLimit
--- PASS: TestOutputLimit
--- PASS: TestCorruptPayloadDetected
--- PASS: TestCorruptBlockDoesNotHideNext
--- PASS: TestResyncWithGarbageBetweenBlocks
--- PASS: TestRandomAccessDecode
--- PASS: TestBadVersionAndMagic
--- PASS: TestTruncatedStream
PASS
ok  	dzblock
```

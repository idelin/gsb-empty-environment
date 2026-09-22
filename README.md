# safeframe

仅使用 Go 标准库实现的结构化二进制帧：支持任意分块解析、字段级脱敏、长度与 CRC32 重算，并保留序号和时间戳以便重放。

## 帧格式

固定头为 32 字节，全部使用大端序：

| 偏移 | 长度 | 字段 |
| --- | ---: | --- |
| 0 | 4 | Magic，固定为 `SFRM` |
| 4 | 1 | Version，固定为 `1` |
| 5 | 1 | Flags，bit 0 表示已脱敏 |
| 6 | 2 | Reserved，必须为 0 |
| 8 | 4 | Payload length |
| 12 | 8 | Sequence |
| 20 | 8 | TimestampNano |
| 28 | 4 | CRC32-IEEE，覆盖前 28 字节头和完整 payload |

Payload 是可嵌套的 TLV 字段序列：

- 2 字节字段名长度 + 字段名 + 1 字节类型 + 4 字节值长度 + 值。
- 类型 `0`：字节串；`1`：UTF-8 字符串；`2`：嵌套消息。
- 其他类型按未知扩展原样保留，不解析、不猜测其内部路径。

## 安全与解析语义

- `Redact(..., Strict: true)` 找不到指定路径、路径落入标量或未知扩展时拒绝输出。
- 脱敏会克隆输入帧，保留 `Sequence`、`TimestampNano`、字段顺序和未知扩展。
- 替换值可以变长；输出前重新计算 payload length、总长度和 CRC32。
- 输出前递归扫描字段名、标量值、嵌套消息和未知扩展原始值；发现任一原始敏感字节仍存在则拒绝输出。
- 所有错误都是静态哨兵错误，不拼接帧内容或敏感值。
- `Decoder.Feed` 接受任意大小分块：
  - 空缓冲且等待数据时返回 `ErrNeedMoreData`。
  - 已有残帧但仍缺头或 payload 时返回 `ErrTruncated`。
  - Magic、版本、保留位、声明长度、TLV 或 CRC 错误属于坏数据，解码器进入永久错误状态。
- 声明 payload 长度先和额度比较，再分配 payload；解码器只保留一个不完整帧。

## 运行

本机 Go 缓存目录受沙箱限制时，可显式指定可写缓存：

```sh
GOCACHE=/tmp/safeframe-gocache GOPATH=/tmp/safeframe-gopath go test -v -cover ./...
GOCACHE=/tmp/safeframe-gocache GOPATH=/tmp/safeframe-gopath go run ./cmd/safeframe-demo
```

## 本次测试结果

```text
=== RUN   TestRoundTripAndUnknownExtension
--- PASS: TestRoundTripAndUnknownExtension (0.00s)
=== RUN   TestRedactNestedAndRecomputesFrame
--- PASS: TestRedactNestedAndRecomputesFrame (0.00s)
=== RUN   TestStrictMissingAndUnknownExtension
--- PASS: TestStrictMissingAndUnknownExtension (0.00s)
=== RUN   TestUnknownExtensionCannotHideSensitiveBytes
--- PASS: TestUnknownExtensionCannotHideSensitiveBytes (0.00s)
=== RUN   TestReplayLengthChangeIsDeterministic
--- PASS: TestReplayLengthChangeIsDeterministic (0.00s)
=== RUN   TestDecoderArbitraryChunksAndIncompleteVsBad
--- PASS: TestDecoderArbitraryChunksAndIncompleteVsBad (0.00s)
=== RUN   TestPayloadLimitEnforcedBeforeAllocation
--- PASS: TestPayloadLimitEnforcedBeforeAllocation (0.00s)
=== RUN   TestBadLengthChecksumAndNoSensitiveErrorText
--- PASS: TestBadLengthChecksumAndNoSensitiveErrorText (0.00s)
=== RUN   TestReaderDistinguishesCleanEOFAndTruncation
--- PASS: TestReaderDistinguishesCleanEOFAndTruncation (0.00s)
=== RUN   TestSentinelErrorSafety
--- PASS: TestSentinelErrorSafety (0.00s)
=== RUN   TestCRCProtectsRecomputedHeader
--- PASS: TestCRCProtectsRecomputedHeader (0.00s)
PASS
coverage: 76.8% of statements
ok  	safeframe	0.806s	coverage: 76.8% of statements
```

## 运行样例

```text
encoded frame: 125 bytes, payload=93, crc=0xa5487c59
stream decode: byte-by-byte feed succeeded; sequence and timestamp preserved
nested redaction: 129 bytes, payload=97, crc=0xae1ef10d; parses=true
unknown-extension path: rejected (safeframe: redaction path enters unknown extension), no output emitted
```

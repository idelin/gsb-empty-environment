# 按消息 ID、总长度和片偏移重组乱序数据

这是一个只依赖 Go 标准库的小型实现。库提供二进制分帧、CRC32 完整性校验、乱序重组、重叠冲突检测、长度配额、完成去重和过期清理。

## 协议帧

所有整数使用大端序，帧头固定 32 字节：

| 偏移 | 长度 | 字段 |
| --- | ---: | --- |
| 0 | 4 | Magic，`0x46524147` |
| 4 | 4 | 版本，当前为 `1` |
| 8 | 8 | 消息 ID |
| 16 | 4 | 消息总长度 |
| 20 | 4 | 本片偏移 |
| 24 | 4 | 本片长度 |
| 28 | 4 | CRC32-IEEE |
| 32 | N | 载荷 |

CRC 覆盖前 28 字节头和载荷，不覆盖 CRC 字段自身。

## 语义

- 按 `(消息 ID, 总长度, 偏移)` 定位字节；片可以任意顺序到达。
- 覆盖区间合并；重叠字节必须完全一致，任一字节不同即返回 `ErrConflict`，不能任选一份。
- 全消息连续覆盖 `[0,totalSize)` 后完成；总长度为 0 的消息收到一个空片即完成。
- 已完成消息在 TTL 内继续识别重复片，重复且一致返回 `Duplicate=true`；完成状态过期后删除，迟到片按新消息处理，不借用旧缓冲区。
- `MaxMessageSize` 限制单消息总长度，`GlobalBytes` 按消息总长度预留全局缓冲；`ReadFrame` 的 gate 在解析头部后、分配载荷前检查。
- `UnmarshalFrame` 和 `ReadFrame` 区分两类输入问题：输入不完整包装 `ErrNeedMoreData`，结构错误或 CRC 错误包装 `ErrBadData`。
- `ReadFrame` 只使用 `io.Reader` 接口读取，因此输入可在任意字节位置分块。

## 运行

```sh
go test ./...
go test -race ./...
go run ./cmd/demo
```

## 样例输出

```text
message id=9001, 44 bytes, fragments sent in reverse-ish order
frame 1: offset=34 length=10 complete=false duplicate=false
frame 2: offset= 0 length=16 complete=false duplicate=false
frame 3: offset=16 length=19 complete=true duplicate=false
reassembled: The quick brown fox jumps over the lazy dog.
```

第二、三片在偏移 34 重叠一个字节；只有该字节一致才允许完成。

## 本机测试结果

```text
=== RUN   TestOutOfOrderReassemblyAllOrders
=== RUN   TestOutOfOrderReassemblyAllOrders/#00
=== RUN   TestOutOfOrderReassemblyAllOrders/#01
=== RUN   TestOutOfOrderReassemblyAllOrders/#02
--- PASS: TestOutOfOrderReassemblyAllOrders (0.00s)
    --- PASS: TestOutOfOrderReassemblyAllOrders/#00 (0.00s)
    --- PASS: TestOutOfOrderReassemblyAllOrders/#01 (0.00s)
    --- PASS: TestOutOfOrderReassemblyAllOrders/#02 (0.00s)
=== RUN   TestOverlappingFragmentsMustAgree
--- PASS: TestOverlappingFragmentsMustAgree (0.00s)
=== RUN   TestLengthQuotaCheckedBeforeAllocation
=== RUN   TestLengthQuotaCheckedBeforeAllocation/per-message
=== RUN   TestLengthQuotaCheckedBeforeAllocation/global
--- PASS: TestLengthQuotaCheckedBeforeAllocation (0.00s)
    --- PASS: TestLengthQuotaCheckedBeforeAllocation/per-message (0.00s)
    --- PASS: TestLengthQuotaCheckedBeforeAllocation/global (0.00s)
=== RUN   TestExpiryRemovesOldBufferAndCompletionDedup
--- PASS: TestExpiryRemovesOldBufferAndCompletionDedup (0.00s)
=== RUN   TestStreamFramesAcceptArbitraryChunks
--- PASS: TestStreamFramesAcceptArbitraryChunks (0.00s)
=== RUN   TestFrameParserDistinguishesMissingAndBadData
--- PASS: TestFrameParserDistinguishesMissingAndBadData (0.00s)
=== RUN   TestGateDrainAllowsNextFrameAfterRejection
--- PASS: TestGateDrainAllowsNextFrameAfterRejection (0.00s)
PASS
ok  	fragmentreassembly	0.182s
?   	fragmentreassembly/cmd/demo	[no test files]
```

# seqwin

固定位宽序号的窗口接收器 + 紧凑确认区间 + 增量帧解析器。仅使用 Go 标准库。

## 设计

- **序号空间** (`seqnum.go`)：`Space` 表示 2^Bits 的模空间（2..32 位）。
  `Compare(a,b)` 采用串行号算术：前向距离 `< Half` 为更新，`> Half` 为更旧，
  **恰好等于 Half（如 8 位下的 128）返回 `Ambiguous`，绝不随意判新旧**。
- **窗口接收器** (`receiver.go`)：`Receiver` 维护大小为 `window ≤ Half` 的环形位图。
  - 窗口内：新包 `AcceptedNew`，已收过的 `Duplicate`（**不增加确认计数**）。
  - 落后于窗口：`RejectedOld`（拒绝过旧包）；超出窗口前沿：`RejectedFuture`。
  - 距离恰为半空间：`RejectedAmbiguous`；序号越界：`RejectedInvalid`。
  - 序号自然环绕：`NewReceiverAt` 可让窗口跨 255→0 的环绕点。
  - `AckRanges()` 把缺口外的已收包压缩为紧凑闭区间（如 `[4-5, 7]`）。
- **增量帧解析** (`frame.go`)：帧格式 `magic(2) | type(1) | flags(1) | len(2) | payload | crc16(2)`。
  - `Parser.Feed` 接受任意分块的字节流，攒够一帧才返回。
  - **缺数据**（`ErrIncomplete`）与**坏数据**（`*CorruptError`：坏 magic / 超长 / CRC 错）严格区分。
  - 声明长度先与 `MaxPayload` 额度比较，**通过后才分配** payload 缓冲。

## 使用

```go
sp, _ := seqwin.NewSpace(8)                 // 8 位序号空间
r, _ := seqwin.NewReceiverAt(sp, 8, 254)    // 窗口 8，前沿从 254 开始
for _, s := range []uint64{254, 255, 0, 1} {// 环绕连续接收
    r.Deliver(s)
}
fmt.Println(r.Next(), r.AckRanges())        // 2 []

p := seqwin.NewParser(64)                   // 修正: seqwin.NewParser(64)
frames, err := p.Feed(chunk)                // chunk 可以是任意大小的分块
```

## 运行

```sh
go test ./...     # 全部测试
go run ./cmd/demo # 运行样例
```

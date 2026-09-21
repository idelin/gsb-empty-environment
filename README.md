# seqwin

固定位宽序号的窗口接收器与确认范围（紧凑区间），纯标准库实现。

- `seq.go` — 序号空间运算：模 2^Bits 加法、有符号差值、半空间比较；
  差值恰好为半空间（8 位时为 128）时返回 `ErrAmbiguous`，不猜测新旧。
- `receiver.go` — 滑动窗口接收器：拒绝过旧包（`ErrTooOld`）与窗口外
  超前包（`ErrTooFarAhead`），允许环绕，重复包幂等不计数，
  `AckRanges()` 输出紧凑闭区间（累计区间 + 缺口之后的乱序块）。
- `frame.go` — 增量帧解析器：`[uint16be 长度][载荷]`，输入可任意分块；
  数据不足返回 `ErrNeedMore`，声明长度超额度返回 `FrameTooLargeError`，
  且额度检查发生在任何载荷大小的分配之前。

## 运行

```sh
go test ./...   # 测试
go run ./cmd/demo
```

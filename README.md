# ringloan

面向音频流水线的定容环形缓冲区：生产者写入字节流，消费者以**租借（Loan）**形式借出连续缓冲区读取。

## 语义

- **跨环尾双片段**：租借跨越环尾时暴露为 `First`（尾段）+ `Second`（环绕头段）两个切片，否则 `Second` 为 nil。
- **不可覆盖**：未释放的租借占据容量，生产者永远无法覆写其字节。
- **乱序释放，前缀归还**：租借可任意顺序释放；但容量仅按**连续已释放前缀**归还给生产者——被未释放老租借堵住的后续释放不归还容量。
- **重复释放报错**：第二次 `Release` 返回 `ErrDoubleRelease`。
- **可取消等待**：`WriteContext` / `LoanContext` 阻塞等待空间或数据，`ctx` 取消时返回 `ctx.Err()` 且不产生副作用。

## 验证（字节序列模型）

测试以模型流 `stream[i] = byte(i % 251)`（质数模，与容量无公因子，偏移错误必现）校验：

- `TestWrapAroundLoan` — 跨环尾租借的双片段内容与模型一致。
- `TestOutOfOrderRelease` — 乱序释放下 `Free()` 严格等于连续已释放前缀推导值；被堵期间生产者写入超时失败。
- `TestDoubleRelease` — 重复释放返回 `ErrDoubleRelease` 且不破坏账目。
- `TestCancelProducerWait` / `TestCancelConsumerWait` / `TestWaitUnblockedByRelease` — 等待可取消、取消无副作用、释放可唤醒生产者。
- `TestRandomizedModel` — 单 goroutine 随机交错写/借/乱序释放 3000 步，每步比对 `Free()` 与模型、租借内容与字节序列。
- `TestConcurrentByteSequence` — 生产者/消费者并发，消费者在释放时校验未释放租借从未被覆写。

运行：`go test -race .`

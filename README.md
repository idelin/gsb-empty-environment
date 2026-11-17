# 异步取消屏障契约

安全检查取消后仍收到成功回调，调用方可能误放行请求。写供Go测试宿主调用的契约监视包，事件含发起、取消请求、取消确认、结果生成和结果消费；取消确认构成屏障，其后不得消费该操作结果，确认前消费与取消按明确先后记录。支持操作ID复用的代次，用所有短事件排列验证违规位置及正常竞争。

## 用法

```go
import netsec "example.org/netsec/q0401"

monitor := netsec.NewMonitor()
monitor.Start("scan-42", 1)
monitor.CancelRequest("scan-42", 1)
monitor.CancelConfirm("scan-42", 1)
monitor.ResultGenerated("scan-42", 1)
outcome := monitor.ResultConsumed("scan-42", 1)

if outcome.Violation != nil {
    // 拒绝放行；outcome.Violation.Index 指向违规的消费事件。
}
```

批量校验可以直接调用 `netsec.Check(events)` 或 `netsec.Analyze(events)`。

## 语义

- `Start`、`CancelRequest`、`CancelConfirm`、`ResultGenerated`、`ResultConsumed` 是五类规范事件；字符串形式也接受 `cancel_requested`、`result_produced` 等常见变体。
- `CancelConfirm` 对同一 `(OperationID, Generation)` 构成单向屏障。屏障前消费是允许的竞争；屏障后消费会生成 `ViolationConsumeAfterCancelConfirmation`。
- 每个 `ResultConsumed` 都有一条 `Consumption` 审计记录，明确标出发生在取消请求前、请求与确认之间还是确认之后。
- `ResultGenerated` 可以晚于取消确认到达；违规点是结果被消费的位置，而不是回调到达的位置。
- 同一操作 ID 可以按 `Generation` 复用，屏障和结果都按代次隔离。省略代次时，新的 `Start` 会自动推进代次。
- 生命周期异常（例如无请求的确认、重复消费）记录在 `Report.Anomalies`，不会与屏障违规混淆。
- `Monitor.Record` 并发安全，事件顺序由互斥锁下的记录顺序和单调 `Sequence` 明确给出。

## 验证

`contract_test.go` 枚举五类短事件的全部 120 种排列，逐项校验违规位置、屏障前正常竞争和消费顺序；另有操作 ID 代次复用与并发确认/消费竞争测试。

# 恶意压缩帧预算跨重试

接入服务允许发送端重试失败的压缩片段，攻击者可能反复触发高成本解压却不增加已接收字节计数。写供Go解压宿主调用的预算组件，对实际输入读取、输出生成和字典操作分别累计成本，失败重试沿用同一请求预算，只有新授权请求才重置。解压器以可注入步骤接口给出，输出先隔离至片段校验成功。用失败在最后一步的重复片段验证不会无限消耗CPU或泄漏未认证输出。

## 组件

- `budget.go`：请求级 `Budget`，三类成本（`CostInput` / `CostOutput` / `CostDict`）独立累计、原子记账、超限整笔回滚；`BindRequest` 按 `RequestID` 区分重试与新授权请求。
- `processor.go`：`Processor` 门面 + 可注入的 `Decompressor` 步骤接口、`DecompressorFactory`、`Verifier`；输出隔离，校验通过才提交副本。
- `processor_test.go`：含核心安全用例 `TestRetrySharedBudgetBoundedCPUNoLeak`。

## 安全语义

- 记账依据解压器自报的**实际工作量**，与网络层已接收字节无关，重放小载荷一样累计。
- 相同 `RequestID` 的失败重试沿用同一预算；预算只在新 `RequestID`（新授权请求）时清零。
- 预算预检前置到解压器分配之前：耗尽后的重试零分配、零解压步骤，CPU 有确定上界。
- 未通过 `Verifier` 的输出只存在于每次尝试的隔离缓冲，函数返回 `nil` + `ErrVerificationFailed`，无未认证字节泄漏。

## 宿主集成示例

```go
budget := netsec.NewBudget(authorizedRequestID, netsec.Limits{
    MaxInputBytes:  1 << 20,
    MaxOutputBytes: 16 << 20,
    MaxDictOps:     1 << 16,
})
proc := netsec.NewProcessor(budget, netsec.DecompressorFunc(newFlateDecompressor), checksumVerifier)

out, err := proc.Process(ctx, netsec.Fragment{
    RequestID: authorizedRequestID, // 重试时保持不变；新授权请求才换新值
    Payload:   chunk,
})
switch {
case errors.Is(err, netsec.ErrBudgetExceeded):
    // 拒绝该请求的后续重试
case errors.Is(err, netsec.ErrVerificationFailed):
    // 片段不合法；输出已被丢弃，可在预算内重试
case err != nil:
    // 其他失败
default:
    deliver(out) // 仅此时 out 才是已认证输出
}
```

## 验证

```sh
go vet ./...
go test -race -count=1 ./...
```

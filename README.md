# SNMPv3 USM时间窗与发现

可导入的 SNMPv3 USM 时间窗实现，根包为 `example.org/netsec/q0718`，也可使用
`example.org/netsec/q0718/usm`。

## 使用

调用方持有本地 `State` 并提供认证回调。`Process`/`Check` 只有在认证成功且
时间窗校验通过后才提交状态；失败路径不会修改 `EngineBoots` 或 `EngineTime`。

```go
local := netsec.NewState(engineID, engineBoots, engineTime)
result := local.Process(msg, func(m netsec.Message) bool {
	return verifyDigest(m) // 调用方完成认证摘要校验
})
if !result.Accepted {
	sendReport(result.Report) // Report 包含 RFC 3414 报告 OID
}
```

`State` 是调用方拥有的值；`Evaluate`/`Check` 返回更新后的
`Result.State` 但不修改入参，`Process`/`ProcessMessage` 在接受报文时提交到
`*State`。成功报文只能推进 `EngineTime`，不能把本地时间回拨。

## 安全规则

- 时间窗为闭区间：相差 150 秒可接受，151 秒返回 `notInTimeWindow`。
- `engineBoots` 与本地不一致时返回 `notInTimeWindow`，包括重放较小的值。
- 本地 `engineBoots == 2147483647` 时拒绝认证报文。
- 未知 `engineID` 在查询用户之前返回 `unknownEngineID`，且不调用用户回调。
- 用户不存在与认证失败返回完全相同的认证失败报告，防止用户名枚举。

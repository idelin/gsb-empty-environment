# NOTIFY与IXFR来源链校验

攻击者向从服务器伪造 NOTIFY 触发无谓刷新，或在 IXFR 应答里夹带伪造差异。给Go辅助区服务器写供区传送入口调用的准入包：NOTIFY 只有源地址属于该区配置的主服务器、且调用方注入的 TSIG 验证通过才触发刷新；IXFR 应答首条 SOA 序列号按 RFC 1982 比较，大于本地才处理，等于视为已最新，小于或恰好相差 2^31（比较无定义）丢弃；差异段以旧 SOA 开始、新 SOA 结束，末条 SOA 须与首条相同，上段结束序列号等于下段起始序列号，任何断链整体丢弃并改发 AXFR，区内容不得被部分改写。用伪造源 NOTIFY、序列号回绕、断链和合法增量验证。

## 调用方式

```go
// NOTIFY：源地址与 TSIG 结果必须同时通过。
decision := netsec.AdmitNotify(zoneConfig, sourceAddr, tsigVerified)
if decision.Refresh {
    // 触发本区刷新。
}

// 也可注入 TSIG 验证回调；不可信源地址不会调用该回调。
decision = netsec.AdmitNotifyWithVerifier(zoneConfig, sourceAddr, verifyTSIG)

// IXFR：先完整校验，再一次性提交；失败时 zone 保持不变。
decision, err := zone.ApplyIXFR(ixfrRecords)
switch decision.Status {
case netsec.IXFRApply:
    // 已原子应用。
case netsec.IXFRUpToDate:
    // 已是最新版本。
case netsec.IXFRFallbackAXFR:
    // 断链或结构错误，整体丢弃并改发 AXFR。
case netsec.IXFRDiscard:
    // 序列号旧于本地或比较无定义，丢弃，不回退 AXFR。
}
```

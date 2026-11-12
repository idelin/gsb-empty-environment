# 认证窗口跨时钟域

客户端与服务端分别给出有效期，直接比较绝对时间会错误放行。实现供Go认证宿主调用的区间时间校验包，各时钟偏移为已知闭区间，只有所有允许偏移下凭据均在半开有效期内才确定接受；部分有效返回待校时，全部无效拒绝。共享时钟约束必须联合使用，用小整数偏移域穷举核对边界与矛盾校时。

## 模型

- 时钟读数约定：`reading = trueTime + offset`，`offset` 为整数秒，真实偏移落在已知闭区间 `[MinOffset, MaxOffset]`。
- 有效期为半开区间 `[NotBefore, NotAfter)`：下界包含、上界排除。
- 校验事件由服务端观测锚点 `Now` 定位，其真实时间为 `ServerNow - offset(server)`；每个窗口都在同一真实时间下判定。
- 三态结论：
  - `VerdictAccept`：联合偏移域内每个赋值下所有窗口同时成立；
  - `VerdictResync`（待校时）：仅部分赋值成立，`Report.Calibration` 给出各时钟仍可行的偏移集合；
  - `VerdictReject`：没有任何可行赋值使凭据有效。
- 同一个 `*Clock` 的全部窗口共享一个偏移变量，按笛卡尔积联合枚举，禁止逐次比较独立取偏移。
- 偏移域为空（`MinOffset > MaxOffset`）即矛盾校时，返回 `ErrInfeasibleOffsets`；联合域超过 `maxCombinations`（1e6）返回 `ErrDomainTooLarge`。

## 用法

双时钟凭据（客户端签发窗口、服务端只持有本机读数）：

```go
report, err := netsec.CheckCredential(netsec.Credential{
    ClientClock: &netsec.Clock{Name: "client", MinOffset: -2, MaxOffset: 2},
    ServerClock: &netsec.Clock{Name: "server", MinOffset: -1, MaxOffset: 1},
    NotBefore:   issuedAt,
    NotAfter:    issuedAt + ttl,
    ServerNow:   serverNow,
})
switch report.Verdict {
case netsec.VerdictAccept:
    // 所有允许偏移下均有效
case netsec.VerdictResync:
    // 依据 report.Calibration 触发校时，或按策略拒绝
case netsec.VerdictReject:
    // 全部偏移下均无效
}
```

多窗口/多时钟的通用入口是 `Problem.Evaluate`；`AnalyzeSkew` 提供双时钟场景的区间化校时分析，其结论与穷举引擎一致（由差分测试守护）。

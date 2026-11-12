# FIX会话序号重放守卫

攻击者重发FIX旧报文，或在Logon里带ResetSeqNumFlag(141)=Y清零序号。给Go接入网关写供会话读取循环调用的序号守卫：MsgSeqNum(34)偏小且PossDupFlag(43)不为Y就断开；为Y时OrigSendingTime(122)不得晚于SendingTime(52)；跳号触发ResendRequest；重置仅在调用方允许时接受。用旧报文、跳号和非法重置验证。

## 设计

每个 FIX 会话持有一个 `Guard`，读取循环每收到一条原始报文就调用一次
`Guard.Handle(raw)`，按返回的 `Verdict.Action` 处理：

- `ActionAccept`：序号合法，投递给业务层；守卫已按需推进期望序号。
- `ActionResend`：检测到跳号，用 `Verdict.BeginSeqNo` 作为
  BeginSeqNo(7) 发送 ResendRequest(2)（EndSeqNo(16)=0 表示到最新）；
  守卫状态不变，等待对端补发。
- `ActionLogout`：重放或协议违规，发送 Logout(5) 后关闭 TCP 连接；
  `Verdict.Reason`/`Verdict.Err` 给出可审计的原因。

序号规则：

- MsgSeqNum(34) 小于期望值且 PossDupFlag(43) 缺失或为 N：判重放，断开。
- 43=Y 时必须带 SendingTime(52) 与 OrigSendingTime(122)，且 122 不得
  晚于 52（相等允许），否则断开；落后的旧重放不推进序号。
- 34 大于期望值：跳号，触发 ResendRequest。
- ResetSeqNumFlag(141)=Y 只允许出现在 Logon(35=A) 上，且仅当调用方以
  `ResetAllow` 创建守卫时才接受；默认 `ResetReject` 直接断开。

## 用法

```go
guard := netsec.NewGuard(1, netsec.ResetReject) // 会话内默认拒绝序号重置

for {
    raw, err := conn.Read()
    if err != nil {
        return err
    }
    v := guard.Handle(raw)
    switch v.Action {
    case netsec.ActionAccept:
        dispatch(raw)
    case netsec.ActionResend:
        sendResendRequest(v.BeginSeqNo, 0)
    case netsec.ActionLogout:
        log.Warn("fix sequence violation", "reason", v.Reason, "err", v.Err)
        sendLogoutAndClose()
        return
    }
}
```

## 验证

`go test -race ./...` 覆盖：旧报文重放（含 43 缺失/为 N/时间戳倒挂/缺
122）、跳号与 ResendRequest 坐标、完整补发恢复流程、非法与授权的
141=Y 重置、并发读取竞争，以及报文与时间戳解析边界。

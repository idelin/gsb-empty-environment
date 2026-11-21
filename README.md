隧道接收端只传输64位序号的低32位，窗口跨回绕时容易错算高位。实现供Go接收宿主调用的抗重放包，窗口宽度小于2的31次方，完整号取与最高已认证号加一距离最近的64位候选，并列取较小者，越过64位上限拒绝；验签成功后原子提交位图，验签失败不得移动窗口。允许并行验签但同序号只交付一次。用小位宽穷举参考验证回绕、窗口左端、极旧包和两个候选同时完成。

## 使用方式

包名是 `antireplay`。接收宿主只需要把线上收到的低 32 位序号和验签回调交给
`Accept`：

```go
w, err := antireplay.New(1024)
if err != nil {
    return err
}

seq, err := w.Accept(low32, func(full uint64) error {
    // 用 full 验证 MAC/签名；成功返回 nil。
    return verifyPacket(full, packet)
})
if err != nil {
    // ErrReplay、ErrTooOld、ErrOverflow、ErrUnderflow 或 ErrVerification
    return err
}
deliver(seq, packet)
```

如果恢复已有会话，用 `NewWithHighest(width, highest)`；传入的最高号会被视为
已认证并立即标记，之后它的重放会被拒绝。

## 语义

- 窗口宽度必须在 `[1, 2^31-1]` 内。
- 每个完整序号是与 `Highest()+1` 距离最近的、低 32 位匹配的 64 位候选；距离
  相同时取较小者。候选越过 `math.MaxUint64` 返回 `ErrOverflow`，越过零下界
  返回 `ErrUnderflow`。
- `Accept` 先在锁内解析候选并检查窗口，然后在锁外调用验签回调；验签成功后
  再次加锁并原子地推进窗口、置位位图。验签失败不会改变最高号或位图。
- 不同序号可以并行验签。提交阶段互斥：同一序号即使被并发验签多次，也只有一个
  调用能成功返回并交付，其余返回 `ErrReplay`。
- 窗口左端 `Highest()-width+1` 仍可接收（若未见过）；再旧的包返回
  `ErrTooOld`。

## 测试

`go test ./...` 包含小位宽穷举参考实现，覆盖候选选择、连续窗口操作、序号回绕、
窗口左端、极旧包、64 位溢出/下溢、验签失败不移动窗口，以及并发验签时同一
序号只交付一次和两个在途候选同时完成。

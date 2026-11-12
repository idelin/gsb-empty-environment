# 确认准入包

`taskpkg` 供可靠传输宿主记录已发送包号并准入对端确认。核心不变量：

- 确认先由注入的 `Authenticator` 校验连接代次和方向，再检查包号。
- 每个确认区间都是闭区间 `[First, Last]`；区间内每个包号都必须确实已发送。
- 任一个区间非法（空、回绕或包含未发送号），整条确认零生效，不解引用任何重传帧。
- 同一重传帧可被多个已发送包引用；只有首次准入某个未确认包时才减少引用，重复确认是幂等的。
- 同一代次内包号严格递增、不可复用、不回绕。包号空间耗尽后必须 `Migrate` 到更大的连接代次，包号从 0 重新开始。
- 认证失败的确认、错误代次/方向的确认、旧的迁移前确认都不会改变状态。

## 基本用法

```go
auth := taskpkg.AuthenticatorFunc(func(msg taskpkg.AckMessage, want taskpkg.Identity) error {
    if msg.Generation != want.Generation || msg.Direction != want.Direction {
        return errors.New("wrong connection")
    }
    return verifyAuthTag(msg.AuthTag) // 宿主自己的密码学校验
})

admission, err := taskpkg.NewAdmission(auth, taskpkg.Config{
    Generation:        7,
    Direction:         taskpkg.DirectionOutbound,
    PacketNumberLimit: 1 << 16, // 包号范围 [0, 65536)
})
if err != nil {
    return err
}

packet, err := admission.SendNext(frameID) // 首次得到 0，随后 1、2……
if err != nil {
    return err
}

result, err := admission.AdmitAck(taskpkg.AckMessage{
    Generation: 7,
    Direction:  taskpkg.DirectionOutbound,
    Intervals: []taskpkg.Interval{
        {First: packet, Last: packet},
    },
})
var unsent *taskpkg.UnsentPacketError
if errors.As(err, &unsent) {
    // unsent.Number 是整条消息中被拒的最小未发送包号。
    return err
}
```

`AckResult.Packets` 列出本次实际从未确认变为已确认的包，`ReleasedFrames` 列出引用数降到零的帧；`OnFrameReleased` 在准入锁释放后逐帧调用。

## 耗尽与迁移

```go
if _, err := admission.SendNext(frameID); errors.Is(err, taskpkg.ErrPacketNumberExhausted) {
    migration, err := admission.Migrate(8, taskpkg.DirectionOutbound)
    if err != nil {
        return err
    }
    // migration.AbandonedFrames 是旧连接中仍需宿主释放或重发的帧。
}
```

迁移必须使用严格更大的代次。旧代次的包号集合、未确认包和帧引用全部作废；迁移不会调用确认释放回调。

## 测试

测试在小号域（0..7）内逐包维护一份朴素参考模型，随机执行稀疏发送、伪确认、重复/重叠区间、认证失败与迁移，并逐步对比每个包状态、每个帧引用数以及被拒的最小未发送号。

```sh
go test ./...
go test -race ./...
```

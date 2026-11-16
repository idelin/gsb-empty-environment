# io_uring限制注册时序

沙箱宿主要把 io_uring 环交给不可信代码前，用限制注册只留下少数操作，攻击者会抢在限制前提交 SQE、重复注册，或绕过注册操作的白名单。为Go宿主写供环创建流程调用的限制管理包：环必须以禁用状态创建，限制只允许注册一次且之后才启用；限制包含允许的注册操作码、SQE 操作码、允许的标志位与要求的标志位；启用后每个 SQE 的 opcode 须在允许集内，标志不得超出允许位且必须含要求位，之后的注册调用也只允许白名单内的操作码。用未禁用时注册、二次注册、缺要求位、多余标志和启用后注册新文件表验证。

## 使用方式

```go
ring := netsec.NewRing() // 创建后处于 IORING_SETUP_R_DISABLED 状态

err := ring.RegisterRestrictions(netsec.Restrictions{
    AllowedRegisterOps: []netsec.RegisterOpcode{netsec.RegisterFilesUpdate},
    AllowedSQEOps:      []netsec.Opcode{netsec.OpRead, netsec.OpWrite},
    AllowedFlags:       netsec.SQEFlagFixedFile | netsec.SQEFlagIODrain,
    RequiredFlags:      netsec.SQEFlagFixedFile,
})
if err != nil {
    return err
}
if err := ring.Enable(); err != nil {
    return err
}

// 每个提交的 SQE 都会校验 opcode、允许标志位和必需标志位。
if err := ring.SubmitSQE(netsec.SQE{
    Opcode: netsec.OpRead,
    Flags:  netsec.SQEFlagFixedFile,
}); err != nil {
    return err
}

// 启用后的注册操作同样受白名单限制。
err = ring.Register(netsec.RegisterFilesUpdate, fixedFiles)
```

`RegisterRestrictions` 仅能在禁用状态调用一次。`Enable` 必须在其后调用，且不要求注册白名单包含 `IORING_REGISTER_ENABLE_RINGS`。启用后，`Register` 会继续拒绝 `IORING_REGISTER_RESTRICTIONS` 和 `IORING_REGISTER_ENABLE_RINGS` 生命周期操作，避免绕过一次性限制流程。

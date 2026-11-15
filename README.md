# io_uring 限制注册时序

`netsec` 管理受限 io_uring 环的生命周期：

1. `NewRing()` 创建禁用状态的环。
2. `RegisterRestrictions()` 只接受一次策略注册，且只能在禁用状态调用。
3. `Enable()` 在策略存在后启用环。
4. `Submit()` 拒绝禁用态提交，并校验 SQE opcode、允许标志位和必需标志位。
5. `Register()` / `RegisterFileTable()` 只接受策略中的注册操作码。

```go
ring := netsec.NewRing()
err := ring.RegisterRestrictions(netsec.Restrictions{
	AllowedRegisterOps: []netsec.RegisterOpcode{
		netsec.IORING_REGISTER_FILES_UPDATE,
	},
	AllowedSQEOps: []netsec.Opcode{
		netsec.IORING_OP_READ,
		netsec.IORING_OP_WRITE,
	},
	AllowedFlags:  netsec.IOSQE_FIXED_FILE | netsec.IOSQE_IO_DRAIN,
	RequiredFlags: netsec.IOSQE_FIXED_FILE,
})
if err != nil {
	return err
}
if err := ring.Enable(); err != nil {
	return err
}
```

策略在注册时会复制为内部集合，调用方之后修改切片不会放宽已安装的限制。包提供 `IOSQE_*`、`IORING_OP_*` 和 `IORING_REGISTER_*` 常量，供宿主接内核注册路径时使用。

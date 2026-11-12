# 租约回收与在途授权隔离

安全作业租约过期后，回收线程不得把正准备提交的旧作业误认为新租约的持有者。写供Go宿主调用的租约栅栏包，持有者代次与每次续约版本分别单调，提交先登记在途票据，最终提交必须再次验证当前代次；过期回收只终结对应版本。用可控时钟和阻塞提交器验证续约、超时、代次重用及迟到回收不会串作业。

## leasefence 包

`leasefence` 是供 Go 宿主调用的租约栅栏实现，核心保证：

- **双单调计数器**：`Acquire` 使持有者代次（generation）严格递增；同一代次内每次 `Renew` 使续约版本（version）严格递增，二者组成不可混淆的栅栏令牌。
- **在途票据先登记**：作业开始准备提交前必须 `Begin` 登记票据（带唯一 ID），票据快照登记时的 `(generation, version)`。
- **提交时再次验证**：`Commit` 的准备回调在栅栏锁之外执行（阻塞提交不会卡住回收线程），准备结束后在锁内重新校验代次仍为持有者、精确版本未被终结、租约未超时且票据仍在途；失败即回滚，不得应用到外部资源。
- **回收只终结对应版本**：续约只提升版本、不终结旧版本（已在途作业仍可提交）；`ReapExpired` 仅在当前令牌仍匹配且已过截止时间时终结该精确 `(generation, version)`，迟到回收键与当前令牌不符时为空操作，绝不误伤新持有者。

### 宿主集成协议

```go
handle, _ := fence.Acquire()
ticket, _ := fence.Begin(handle)

err := fence.Commit(ticket, func(tok leasefence.Token) error {
    // 对外部资源做可回滚的两阶段准备，允许阻塞
    return external.Prepare(tok.Generation(), tok.Version())
})
if err != nil {
    external.Rollback() // ErrFenced / ErrExpired：旧作业绝不能落地
    return
}
external.Apply() // 最终校验通过后才允许修改共享资源
```

### 测试

测试使用 `ManualClock`（可控、单调、并发安全）与 `blockingCommitter`（阻塞在闸门上的提交器），覆盖续约保活、精确超时回收、代次重用隔离、迟到回收空操作、后台 reaper 与并发竞态（`go test ./... -race`）。

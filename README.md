# 密码验证负载隔离

认证服务用高成本密码校验保护账号，却可能被未知账号请求耗尽所有工作线程。实现供Go登录宿主调用的验证调度包，账号查找及校验由回调给出，未知账号走固定虚拟记录；每租户有独立并发上限和全局内存令牌，取消必须区分排队与已经开始的不可中断校验。对外仅在统一最短期限后返回同类失败。用假时钟和阻塞校验器核对资源守恒、饥饿与取消后的敏感缓冲清理。

## scheduler 包

实现位于 `scheduler/`，模块路径 `example.org/netsec/uid1267/scheduler`，仅依赖标准库。

### 宿主接入

```go
s, err := scheduler.New(scheduler.Config{
    PerTenantCap: 8,                          // 每租户在途校验上限
    GlobalTokens: 256,                        // 全局内存令牌总数
    MinDelay:     250 * time.Millisecond,     // 统一最短外部耗时
    Lookup:       lookupFn,                   // 账号查找回调
    Verify:       verifyFn,                   // 高成本密码校验回调
    Dummy:        &scheduler.Account{ID: "dummy"}, // 未知账号的固定虚拟记录
    // Clock 留空使用真实时钟；测试可注入 FakeClock
})
if err != nil { /* 配置非法 */ }

err := s.Authenticate(ctx, tenantID, accountID, secret)
// 失败永远是 scheduler.ErrDenied，无法区分未知账号、密码错误或查找失败
```

### 关键语义

- **虚拟记录**：`Lookup` 返回未命中或报错时，调度器改用配置的固定 `Dummy`
  记录并照常执行完整的高成本 `Verify`，使未知账号与真实账号消耗相同资源、
  耗时一致，无法用于枚举账号或廉价耗尽工作线程。
- **两级准入**：每租户一个 FIFO 公平信号量（`PerTenantCap`），叠加一个全局
  FIFO 内存令牌池（`GlobalTokens`）。两个槽位都拿到后才开始查找与校验，
  单租户打满不会挤占其他租户的配额，全局在途数永不超过令牌数。
- **取消分两阶段**：
  - 排队阶段（租户或全局信号量等待中）取消：立即返回 `ctx.Err()`，不启动
    任何校验、不占用槽位。
  - 一旦准入，查找与校验通过 `context.WithoutCancel` 不可中断地跑完；调用方
    取消只会解除结果等待，终端结果仍在校验完成、且满足最短期限后返回。
- **统一最短期限**：所有认证终端结果（成功或失败）都至少持有 `MinDelay` 才
  返回，失败统一为 `ErrDenied`，消除时序与错误信息侧信道。
- **敏感缓冲**：调度器复制入站密钥到私有切片，任何退出路径（含排队取消）
  都会清零该副本。

### 测试

`scheduler/scheduler_test.go` 使用 `FakeClock`（手动推进时间）和每次调用独立
门控的阻塞校验器，覆盖：

- 未知账号走虚拟记录、失败同类且受最短期限约束；
- 租户槽位、全局令牌、goroutine 与密钥副本在完成和两类排队取消后全部归零
  （资源守恒、缓冲清零、无 goroutine 泄漏）；
- 严格 FIFO 无饥饿、租户配额隔离、全局上限不被突破；
- 排队取消与在途不可中断取消的语义区分；
- 配置校验。

运行：`go test -race ./...`

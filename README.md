# fetchquota

多站点抓取共用配额，一个站点收到Retry-After时常拖停其他账号。用Go实现供抓取器调用的分层配额调度包，同时满足主机和账号额度、截止时间和每个队列的先来先服务。使用注入时钟；取消已获准但还没发送的请求必须归还两级额度。用竞争请求证明冷却隔离及边界时刻不会重复发放。

## 模型

每个请求同时消耗两级令牌桶：

- **主机桶**：同一目标主机的所有账号共享，防止单站点被打爆。
- **账号桶**：每个抓取账号独立，防止单账号超速。

只有两级桶都有令牌时请求才会获准。每个 `(账号, 主机)` 组合是一条独立 FIFO 队列，
只有队首请求能参与令牌竞争，因此队列内严格先来先服务，后来者无法插队。

`Retry-After` 通过 `Cooldown` 只冷却对应的那一条 `(账号, 主机)` 队列：
同一主机上的其他账号、同一账号访问的其他主机都不受影响。

## 时钟

调度器不直接调用 `time` 包，而是依赖注入的 `Clock`：

- 生产环境用 `NewRealClock()`（基于墙钟）。
- 测试用 `NewFakeClock()`，时间只有在显式调用 `Advance` 时才前进，
  从而可以确定性地断言冷却结束、令牌补齐等边界时刻。

## 用法

```go
sched, err := fetchquota.New(fetchquota.NewRealClock(), fetchquota.Config{
    Hosts:    map[string]fetchquota.RateLimit{"api.example.com": {Rate: 10, Burst: 20}},
    Accounts: map[string]fetchquota.RateLimit{"crawler-1": {Rate: 5, Burst: 10}},
})

// 阻塞获取，或带绝对截止时间：
lease, err := sched.AcquireUntil(ctx, "crawler-1", "api.example.com", deadline)
if err != nil {
    return err // ErrUnknownHost / ErrUnknownAccount / ErrDeadlineExceeded / ctx 错误
}

resp, err := http.Do(req)
if err != nil {
    lease.Release() // 请求未发出：主机和账号两级额度一并归还
    return err
}
if resp.StatusCode == http.StatusTooManyRequests {
    sched.Cooldown("crawler-1", "api.example.com", retryAfter) // 只冷却这一条队列
    lease.Release()
} else {
    lease.MarkSent() // 请求已发出，两级额度保持消耗
}
```

`Lease` 必须且只能终结一次：`MarkSent` 表示已发送（不归还），
`Release`/`Cancel` 表示放弃发送（原子归还两级额度），重复调用安全。

## 关键语义

- **边界不重复发放**：令牌在某一时刻恰好补齐时，先执行发放判断再比较截止时间；
  一个令牌只可能被队首请求取走，竞争请求在同一边界时刻不会重复获得。
- **冷却隔离**：冷却状态记录在队列粒度，竞争的其他账号/主机队列照常发放。
- **取消归还**：已获准未发送的请求取消时，主机桶和账号桶各回补一个令牌并唤醒等待者；
  排队中取消则直接出队，不消耗任何令牌。
- **截止时间**：`AcquireUntil` 的 deadline 相对注入时钟计量；超时返回
  `ErrDeadlineExceeded`，且不会消耗令牌。

## 测试

```sh
go test -race ./...
```

测试覆盖两级额度约束、FIFO 顺序、冷却隔离、冷却与补桶边界的竞争不重复发放、
截止时间边界、取消两级归还、假时钟确定性以及真实时钟下的高并发竞态。

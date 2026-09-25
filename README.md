# ttlcache

软/硬双 TTL 的进程内缓存，仅使用 Go 标准库。数据源、时钟、失败退避全部以函数注入，不依赖任何远程缓存。

## 语义

- **软期内**（`< SoftTTL`）：直接返回缓存值。
- **软过期窗口**（`SoftTTL ~ HardTTL`）：立即返回旧值（`Stale=true`），并触发一次**共享的后台刷新**（single-flight，并发读只合并为一次源调用）。
- **硬过期后**（`> HardTTL`）：读必须阻塞等待新值，绝不继续返回旧值；失败退避只节流软窗口刷新，**不会延长硬过期**。
- 软过期结果携带**来源版本**（`Version`）与**可用期限**（`UsableUntil`，即硬截止）。

## 线性化点

- 新鲜/软过期读：在互斥锁下检查 entry 状态的瞬间。
- 硬过期读：在其等待的刷新完成并发布结果时（等待 entry 的 `done` 通道）。
- 刷新回包：在互斥锁下应用到 entry 时；若 entry 代际已变（被 `Delete`/`Set` 替换或缓存已关闭）则丢弃——旧刷新既不能复活被删除的键，也不能覆盖更新的值。
- 读者取消：`ctx.Done()` 的 select 点；共享刷新不受影响，继续为其他读者服务。
- `Close`：在互斥锁下置关闭位，取消在途刷新并等待所有刷新 goroutine 退出（借用资源全部释放，`InFlight()` 归零）。

## 使用

```go
c := ttlcache.New(ttlcache.Config[string, string]{
    Source:  func(ctx context.Context, key string) (string, error) { ... },
    SoftTTL: 300 * time.Millisecond,
    HardTTL: 900 * time.Millisecond,
    Now:     myClock.Now,              // 可选，默认 time.Now
    Backoff: func(f int) time.Duration { ... }, // 可选，默认指数退避
})
defer c.Close()
r, err := c.Get(ctx, "key") // r.Value / r.Version / r.Stale / r.UsableUntil
```

## 运行

```sh
go test -race ./...   # 测试
go run ./cmd/demo     # 运行样例
```

## 测试覆盖（`cache_test.go`）

- `TestSoftWindowTenReadsOneRefresh`：软期十次并发读只触发一次刷新，结果携带来源版本与可用期限。
- `TestFailureBackoffDoesNotExtendHardExpiry`：失败退避节流软窗口刷新，但硬过期读无视退避、必须等到新值。
- `TestLateRefreshDoesNotOverwriteSet`：旧刷新晚回不覆盖 `Set` 的新值。
- `TestLateRefreshDoesNotResurrectDeletedKey`：旧刷新晚回不复活被删除的键。
- `TestReaderCancellationKeepsSharedRefresh`：读者取消只影响自身，共享刷新继续，资源在 `Close` 时释放。
- `TestCloseCancelsAndReleases`：`Close` 取消在途刷新并等待资源释放，之后 `Get` 返回 `ErrClosed`。
- `TestStateExploration`：可控时钟下的随机状态探索（并发读/时钟推进/Set/Delete），核验每个键代际最多一次共享刷新、过期不越界（`UsableUntil` 严格晚于读取时刻）、关闭后借用资源全部释放。

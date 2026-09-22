# 多租户加权公平调度器

按租户权重分配执行额度，保持各租户任务 **FIFO**，并用一个**全局并发上限**约束所有租户。
仅使用 Go 标准库；任务函数、耗时、失败均由调用方（测试）注入。

## 运行

```bash
go test -race -count=1 ./...      # 竞态检测
go run ./cmd/demo                 # 运行示例（1:2 权重）
```

## 语义

- **权重比例**：每个租户维护一个归一化服务计数 `served/weight`，每次有空槽时选择
  该值最小的非空租户（平局按租户 ID）。持续饱和的租户长期服务次数正比于权重
  （1:2 → 约 1:2，见 `TestSaturatedRatio`）。
- **每租户 FIFO**：每个租户一个 FIFO 队列，只有队首可被派发，租户内严格按提交顺序启动。
- **全局并发**：单一 `running` 计数 + worker goroutine，任何时刻运行任务数 ≤ 上限。
- **空闲租户不浪费槽**：只在非空租户中挑选；某租户无任务时其他租户用满全部槽位
  （`TestIdleTenantDoesNotWasteSlots`）。
- **动态权重**：`SetWeight` 只改变挑选时读取的权重，**仅影响之后的选择**，不回溯补偿
  （`TestPickTenantDynamicWeights`、`TestSetWeightAffectsFutureSelections`）。
- **取消不消耗额度**：`Cancel` 仅能移除仍在等待队列中的任务；它从不启动、不占用槽位、
  不计入 `served`。运行中的任务不被打断（`TestCancelWaitingDoesNotConsumeQuota`）。
- **失败释放槽位**：任务返回的错误完全由调用方注入；无论成功失败，结束都归还全局槽位
  （`TestFailureReleasesSlot`）。
- **关闭**：`Shutdown` 后不再接收新任务（`Submit/AddTenant/SetWeight` 返回 `ErrClosed`），
  但会排空已排队和运行中的任务；支持 context 超时与重复调用
  （`TestShutdownDrainsAndRejects`、`TestShutdownContextTimeout`）。

## 实现要点

单个 dispatcher goroutine 拥有全部可变状态，外部通过带缓冲的 channel 提交事件
（提交/取消/调权/统计/关闭），任务在独立 worker goroutine 中执行，完成后回送 completion
事件。共享状态不被并发直接访问，因此无需锁、也无数据 race；同步原语全部来自标准库
（channel、`context`）。

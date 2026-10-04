# batchq

容量受限的批量任务队列（Go 1.11+，无第三方依赖）。

## 语义

- **两种接纳模式**：`AdmitAll`（整批放得下才全接纳，否则全拒）或
  `AdmitPrefix`（只接纳能放下的最长前缀，其余拒绝）。
- **预留与入队是原子的**：被接纳的任务一定同时占好容量并进队。
- **先到先服务**：批次按到达顺序取号、按号依次做预留，
  后面的任务不会先于前面的任务被接纳。
- **统一退还规则**：`Reservation.Cancel()` 在预留之后的任何时刻调用，
  都走同一条路径——任务出队、容量归还，且只生效一次（幂等）。

## 用法

```go
q := batchq.New(100)

admitted, rejected, err := q.Admit([]batchq.Task{
    {ID: "a", Size: 30},
    {ID: "b", Size: 40},
    {ID: "c", Size: 50},
}, batchq.AdmitPrefix)
// admitted = [a b]，rejected = [c]

admitted[0].Cancel() // 退还 30 单位容量，可重复调用
```

## 并发历史核对

队列把每次接纳/取消按实际发生顺序记录为事件流（`Events()`）。
`TestConcurrentHistories` 用多 goroutine 随机提交批次与取消，
然后回放事件流核对：容量从未超限、每次接纳都是合法（最长）前缀、
批次按序号顺序被服务、各调用方拿到的任务列表与历史一致、
最终占用容量等于仍存活的预留之和。

```sh
go test -race ./...
```

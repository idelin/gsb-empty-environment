# batch — 批次执行区间跟踪库

把总量 `[0, total)` 拆成子区间租约（lease）派发给工作者；工作者可确认
已完成的前缀再失败，重派只包含未确认区间。所有确认以**区间并集**模型
记录，因此：

- **乱序回执**：任意顺序到达的确认都合并进同一个并集，判定一致；
- **重叠确认幂等**：重叠/重复确认不改变覆盖量，不重复计数；
- **越界拒绝**：超出租约范围（含超出总量、空区间、反向区间）返回
  `ErrOutOfBounds`；
- **租约更替**：`Fail` 后旧租约 ID 的迟到回执返回 `ErrStaleLease`，
  未确认余量以新一代租约 ID 重派；已完成租约的重复回执为幂等空操作；
- **完成判定**：当且仅当确认并集完整覆盖 `[0, total)` 时
  `Complete()` 为真。

## 用法

```go
tr, _ := batch.NewTracker(100)

// 派发：按 chunkSize 切分未确认且未被活跃租约占用的区间
leases := tr.Dispatch(10, 0) // [0,10) [10,20) ... [100,100)...

// 工作者确认前缀 [lease.Start, upto)，然后失败
_ = tr.ConfirmPrefix(leases[0].ID, 7)
_ = tr.Fail(leases[0].ID)

// 重派：只含未确认余量 [7,10)，且是新的租约 ID
next := tr.Dispatch(10, 0)

// 任意子区间确认（须在租约范围内）
_ = tr.Confirm(next[0].ID, batch.Interval{Start: 7, End: 10})

tr.Unconfirmed() // 未确认区间（并集的补）
tr.Complete()    // 是否全部完成
```

错误哨兵（Go 1.11，无 `errors.Is`，用 `==` 比较）：
`ErrOutOfBounds`、`ErrUnknownLease`、`ErrStaleLease`。

## 测试

```sh
go test ./...
```

`tracker_test.go` 用朴素布尔数组模型对区间并集实现做随机化交叉验证
（随机派发/确认前缀/失败/伪造回执，每步比对覆盖状态与完成判定）。

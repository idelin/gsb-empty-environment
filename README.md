# takeover

`prescreen.local/ddd-2294-20261122-v2` 是一个可导入的 Go 会话宿主接管协议包。

它把一次终端控制权表示为：

- `ProcessStartID`：进程启动身份，避免 PID 重用；
- `SessionRoot`：调试会话根；
- `SignalSet`：允许发出的信号；
- `DimensionSet`：被委托的终端窗口维度；
- `SeqSet`：一次性序号范围。

序号集合始终用有序区间表示，相邻区间会自动合并；即使范围是
`[0, MaxUint64]`，也不会展开成逐序号内存结构。

## 基本用法

```go
left := takeover.WindowDimension("display-1", "left")
right := takeover.WindowDimension("display-1", "right")

root := takeover.Grant{
    ID:             "root",
    ProcessStartID: takeover.NewProcessStartID("boot-1", 100, 1000),
    SessionRoot:    "debug-session",
    Signals:        takeover.Signals(takeover.SignalInterrupt),
    Dimensions:     takeover.Dimensions(left, right),
    Sequences:      takeover.SeqRangeSet(1, 200),
}

host, err := takeover.NewHost(root,
    takeover.WithSharedHMACKey("session-key", []byte("shared secret")))
```

跨进程或跨宿主校验回执时，双方必须使用同一 HMAC key，或通过
`WithSigner`/`WithVerifier` 注入彼此信任的签名器。

同一显示上的两个窗口可以并行借出；同一个窗口不能同时借给两个子端。

```go
a, err := host.Delegate("root", takeover.DelegateRequest{
    GrantID:        "child-a",
    ProcessStartID: takeover.NewProcessStartID("boot-1", 101, 1001),
    SessionRoot:    "debug-session",
    Signals:        takeover.Signals(takeover.SignalInterrupt),
    Dimensions:     takeover.Dimensions(left),
    Sequences:      takeover.SeqRangeSet(1, 50),
})
```

## 归还与门禁

`Host.Return` 支持整次归还或部分归还：

```go
result, err := host.Return(takeover.ReturnRequest{
    GrantID:   "child-a",
    Partial:   true,
    Sequences: takeover.SeqRangeSet(1, 10),
    Receipts:  []takeover.ReturnReceipt{descendantReceipt},
})
```

归还范围中所有仍活跃的后代都必须满足下列条件之一：

- 提供该后代自己的签名回执，且回执覆盖其受影响范围；
- 由祖先逐段封存该后代在受影响范围内的全部未消费序号，并撤销该切片的维度。

只提供直接子端回执不够。孙端或其他未完成后代同样需要回执或封存证据，
否则返回 `UnsettledError`（`errors.Is(err, takeover.ErrUnsettled)` 为真）。

归还后，旧授权中的序号不会重新分配给新作业。迟到的控制消息会因
`GrantID`、进程启动身份、会话根、维度或序号失效而被拒绝。

## 状态收敛

`Reconcile` 接收事件观察值，不依赖到达顺序。对每个序号，只有最高 epoch
的证据有效；同一最高 epoch 上互相冲突的证据保持为“待证明”。

```go
partition, err := takeover.Reconcile(universe, observations...)
```

结果严格分为三个互不重叠的集合：

- `Controlled`：已证实可控；
- `Invalid`：确定失效；
- `Pending`：仍待证明。

`Partition.Validate` 会检查三者不重叠且并集等于 `Universe`。当存在多个
相容视图时，`ReconcileViews` 只保留所有视图一致同意的结论。

## 效果回执

`Host.AuthorizeControl` 会原子消耗一次性序号，并保存可查询的
`EffectReceipt`。相同 `OperationID` 的重复调用是幂等的；相同序号不能
被第二个操作再次消耗。

```go
receipt, err := host.AuthorizeControl(takeover.ControlRequest{...})
stored, ok := host.Receipt(receipt.OperationID)
stored, ok = host.ReceiptForSequence("root", 17)
```

## 崩溃恢复

`Emitter` 在本地授权前和发送前分别写入 journal：

1. `prepared`：已记录意图，尚未发送；
2. `dispatched`：已经交给传输层，结果可能不确定；
3. `confirmed`：接收者回执已确认；
4. `in_doubt`：无法证明是否已生效。

重启后 `Emitter.Recover` 对 `dispatched`/`in_doubt` 记录先查询接收者回执。
如果回执存在，直接完成确认且不重发；如果不存在，默认保持 `in_doubt`，
避免重复触发信号。只有 `prepared` 记录允许自动继续发送。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖了跨层部分归还、相邻区间合并、同一显示 ID 的两个窗口、进程号
重用、乱序分区收敛、大范围不展开，以及发送后本机中断的无重放恢复。

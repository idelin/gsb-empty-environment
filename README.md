# 内容审核分片聚合拒绝绕过

安全网关把大对象分片交给检测器，某片通过不能代表整份对象通过。实现供Go网关调用的判定聚合包，清单固定片号、长度和摘要，检测结果绑定清单代次与完整检查集合；全部片完成且无阳性后才发布，缺片、超时和失败返回未完成，迟到旧清单结果忽略。支持同片多个检测器，使用可控事件测试重复回执、片段替换和最后一个结果与取消竞争。

## 设计

- `Manifest` 固定每个分片的片号、长度、摘要、完整检查集合与单调递增的 `Generation`；
  分片替换通过安装更新代次的清单完成。
- `Receipt` 绑定代次、片号、检查项与判定。旧代次、未知片/检查、终态后的回执
  （含重复回执）一律忽略，首个有效判定生效，槽位不重复计数。
- 仅当当前代次的每个 `(片, 检查)` 槽位都收到非阳性回执才发布 `StatusPass`；
  阳性立即 `StatusBlocked`；检测器失败、超时、取消以及仍有缺片时均为
  `StatusIncomplete`。
- 纯事件 reducer（`machine.go`）承载全部状态规则，无线程/时间依赖；
  `Aggregator` 加锁串行化网关并发调用，并通过 `Scheduler` 调度超时。
- `FakeScheduler` 用手动 `Advance` 驱动确定性事件顺序，可脚本化重复回执、
  片段替换、末结果与取消/超时竞争；生产环境默认使用墙钟计时器。

## 用法

```go
agg := netsec.NewAggregator()
_ = agg.InstallManifest(netsec.Manifest{
    Generation: 1,
    Chunks:     []netsec.ChunkSpec{{Index: 0, Length: 1024, Digest: netsec.Digest{Algorithm: "sha256", Hex: "..."}}},
    Checks:     []netsec.CheckID{"av", "dlp"},
    Timeout:    30 * time.Second,
})

// 每个检测器对每片回传一份回执
applied, err := agg.Submit(netsec.Receipt{
    Generation: 1, ChunkIndex: 0, Check: "av", Verdict: netsec.VerdictPass,
})

select {
case decision := <-agg.Final():
    // decision.Status == StatusPass 才允许发布对象
}
```

## 测试

```sh
go vet ./...
go test -race -count=1 ./...
```

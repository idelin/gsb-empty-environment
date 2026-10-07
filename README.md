# ledger

Go 账户账本:可用 / 冻结 / 已结算三桶分离,冻结绑定币种与请求 id,
并发提交按版本裁决,批量跨账户操作原子执行。

## 模型

- **三桶分离**:每个账户按币种维护 `Available` 与 `Frozen`;已结算金额
  离开账户体系,计入全局 settled 台账(`Store.Settled`)。
- **冻结记录**:每次冻结生成 `Freeze{RequestID, Currency, Amount, Remaining}`,
  部分结算后只有 `Remaining` 可继续结算或释放,冻结关闭后拒绝一切操作。
- **版本裁决**:所有变更在快照上计算,经 `commit` 按账户版本 CAS 提交;
  并发结算/释放冲突时只有一个获胜,其余在新快照上确定性重验重试。
- **批量原子**:`Batch` 暂存全部跨账户变更,一次性版本校验提交,
  任一腿失败则整体回滚。
- **守恒核对**:`CheckConservation` 校验每币种
  `supply == Σ(available + frozen) + settled`。
- **故障重放**:每个操作以请求 id 幂等,回执只产生一次;并发重复请求
  经 singleflight 共享同一回执,重放返回相同回执且不重复生效。

## 使用

```go
s := ledger.NewStore()
s.CreateAccount("alice")
s.Deposit("dep-1", "alice", "USD", 1000)
fr, _ := s.Freeze("req-f1", "alice", "USD", 400)   // avail 600 / frozen 400
s.Settle("req-s1", "alice", fr.FreezeID, 150)      // settled 150, remaining 250
s.Release("req-r1", "alice", fr.FreezeID, 250)     // 仅可释放剩余
s.CheckConservation()
```

## 测试

```sh
go test ./ledger/ -race -count=1
```

# scd2merge

数据仓库的维度表要保留历史版本，但上游更新会迟到或重复。给Go入仓组件实现缓慢变化维合并包：每个业务键维护生效区间，迟到的变更插入到正确位置并切分已有区间，相邻相同内容合并，重复事件幂等，删除事件关闭区间；任一时刻每个键最多一个有效版本。打乱到达顺序，与按事件时间全量重建的结果对比。

## 用法

```go
s := scd2merge.New[string]()
s.Apply(scd2merge.Event[string]{ID: "e1", Key: "user-1", At: t1, Op: scd2merge.OpUpsert, Value: "gold"})
s.Apply(scd2merge.Event[string]{ID: "e2", Key: "user-1", At: t2, Op: scd2merge.OpDelete})

ivs := s.Intervals("user-1")   // 生效区间，相邻同内容已合并
v, ok := s.ValueAt("user-1", t) // 任意时刻的有效版本
```

## 语义

- 区间为半开 `[From, To)`，`To` 为零值表示至今有效；任一时刻每个键最多一个有效版本。
- 事件按 `ID` 幂等去重；乱序、迟到、重复到达均安全。
- 同一事件时间戳上删除优先于变更；同刻多个变更按事件 `ID` 大者胜出。
- 以任意顺序 `Apply` 同一批事件，结果与 `Rebuild`（按事件时间全量重建）完全一致；
  `TestShuffledMatchesRebuild` 用随机事件流（含迟到、重复、删除、同刻冲突）验证了该汇合性。

## 测试

```sh
go test ./...
```

# 端口扫描重传消歧

网络探针需要检测低速端口扫描，重传与乱序结果不能把正常客户端误算为扫描器。写供Go检测宿主调用的流式组件，开始、失败、成功事件均携带探针启动代、连接实例号、固定开始时间s及源目标地址端口，元数据冲突拒绝；成功对同一实例的失败具有最终覆盖效力，重复事件不增计数。调用方给定正窗口宽度W、两项正阈值和查询时刻t，窗口明确为按开始时间计的[t-W,t)，分别统计同源对单主机的不同失败端口数、对同端口的不同失败目标数，达到阈值告警。水位线w单调，s<w的任何新增或修正均拒绝并列为迟到；已登记且t>w的查询可因修正撤回告警，w达到t后冻结该窗口。内存超限须标检测不完整。用全量重算核对成功迟到、跨代实例号复用及两端点。

## 组件说明

包 `netsec` 暴露并发安全的流式检测器 `Detector`，全部时间语义基于连接的固定开始时间（事件时间），而非到达顺序。

### 事件与实例

- `Event{Kind, Generation, Instance, Start, Source, Target}`：开始/失败/成功三类事件携带相同的完整元数据。
- 实例键为 `(Generation, Instance)`；同代同实例号的开始时间或任一端点不一致即返回 `IngestConflict`。
- 重复事件返回 `IngestDuplicate` 且不增计数；`EventSuccess` 对同实例失败具有最终覆盖效力，成功之后到达的失败仍是重复事件，不能复活失败计数。
- 实例号可跨代复用，不同代互不影响。

### 窗口与两类告警

查询 `Query{Window: W, PortThreshold, HostThreshold, At: t}` 的窗口严格为开始时间上的 `[t-W, t)`，仅统计解析为失败的实例，并按不同端点去重：

- `AlertHostPortSweep`：同一源 IP 对同一目标主机的不同失败端口数达到 `PortThreshold`。
- `AlertPortTargetSweep`：同一源 IP 对同一目标端口的不同失败目标主机数达到 `HostThreshold`。

### 水位线与迟到

- `AdvanceWatermark(w)` 单调推进；任何 `s < w` 的新增或状态修正返回 `IngestLate`（非改变性的重复仍记为重复并带 `Late=true`）。
- 已登记且 `t > w` 的查询保持开放，成功覆盖等修正可撤回告警（`ChangeWithdrawn`）。
- `w >= t` 后窗口冻结，后续修正不再影响；冻结后才登记的查询在登记时立即冻结。

### 内存与不完整标记

`WithMemoryLimit` 设定近似驻留字节上限；超限插入返回 `IngestMemoryLimit`，检测器与后续所有 `QuerySnapshot.Incomplete` 粘性置真，表示结果可能缺失。

### 全量重算核对

`oracle_test.go` 内置独立的朴素全量重算参考实现，并以 12 个随机种子做差分测试，重点覆盖成功迟到、跨代实例号复用和两端点身份；确定性用例 `TestDeterministicFullRecompute` 单独固化这三类场景。

## 用法示例

```go
d := netsec.NewDetector(
    netsec.WithMemoryLimit(64<<20),
    netsec.WithChangeCallback(func(c netsec.Change) { log.Printf("alert change: %+v", c) }),
)

id, err := d.RegisterQuery(netsec.Query{
    Window:        5 * time.Minute,
    PortThreshold: 20,
    HostThreshold: 10,
    At:            time.Now(),
})
if err != nil {
    log.Fatal(err)
}

switch r := d.Ingest(event); r.Status {
case netsec.IngestConflict:
    log.Printf("metadata conflict on %+v", r.Instance)
case netsec.IngestLate:
    log.Printf("late event on %+v", r.Instance)
case netsec.IngestMemoryLimit:
    log.Printf("memory budget exceeded, results incomplete")
}

d.AdvanceWatermark(time.Now())
snap, ok := d.Snapshot(id)
```

运行测试：

```sh
go test ./... -race
```

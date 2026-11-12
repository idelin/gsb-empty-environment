# HTTP流量方向预算

安全代理对客户端上传与上游下载使用不同预算，隧道切换后容易把流量计入错误方向。实现供Go双工宿主调用的计费层，方向、请求代次和协议阶段固定于字节租约，已预留未发送字节可归还，实际发送不可退账。用短写及升级竞争验证两个方向独立封顶，失败路径不会重复扣费或返还。

## 设计

计费层位于包 `netsec`，核心类型：

- `Meter`：持有上传 `ClientUpload` 与下载 `UpstreamDownload` 两套独立硬封顶，内部加锁，可被双工泵并发调用。
- `Lease`：一次字节预留。`Direction`、请求代次（`Generation`）和 `Phase`（`PhaseHeader`/`PhaseBody`/`PhaseTunnel`）在预留时固定，租约生命周期内不可变。
- `LeasedWriter`：把任意 `io.Writer` 适配为计量写入，方向固定，升级握手获胜后可用 `SetPhase(PhaseTunnel)` 推进协议阶段。

结算规则：

- `Reserve` 按 `已发送 + 已预留` 判定封顶，拒绝时不改任何状态、不触碰底层 writer。
- 未发送的预留字节通过 `Release` 或部分 `Settle` 归还，可再次被预留。
- 已发送字节只进 `committed`，永不退账；`Settle`/`Commit`/`Release` 均为一次性操作，重复调用返回 `ErrLeaseSettled`。
- 隧道切换调用 `Meter.NextGeneration()`；旧租约仍按出生代次结算，新字节计入新代次，`Breakdown` 可按代次与阶段对账。
- `Meter.Charge` 串联预留→写入→结算：短写只结算实际字节、返还余量；写错误同样按实际字节结算，绝不重复扣费或返还。

## 用法

```go
meter := NewMeter(uploadCap, downloadCap)
up, _ := meter.NewWriter(ClientUpload, PhaseBody, upstreamConn)
down, _ := meter.NewWriter(UpstreamDownload, PhaseBody, clientConn)

// CONNECT/升级握手获胜后：
up.SetPhase(PhaseTunnel)
down.SetPhase(PhaseTunnel)
meter.NextGeneration()

if _, err := up.Write(frame); errors.Is(err, ErrBudgetExceeded) {
    // 该方向封顶，底层写入从未发生
}
```

## 测试

```sh
go test -race -count=1 ./...
```

覆盖独立封顶、短写返还、写错误一次性结算、预留拒绝无副作用、代次/阶段固定、零封顶边界，以及双工泵在升级竞争下的对账（线上字节数与 `committed` 完全一致，两个方向各自封顶且不互相借用）。

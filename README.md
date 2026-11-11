# 凭据撤销扇出一致性

微服务会缓存身份凭据的有效性，断线恢复时丢一个撤销事件就会持续放行。写供Go服务调用的撤销订阅包，事件带严格序号和代次，只有连续应用到检查点才提供有效判断；发现缺口进入拒绝状态并请求完整快照，快照和后续增量原子切换。用重复、乱序、快照期间新事件和重启测试，陈旧订阅不能报告已追上最新检查点。

## 实现：`revocation` 包

`revocation/` 提供供 Go 服务嵌入的撤销订阅状态机与断线重连 runner。

### 一致性模型

- 事件携带严格坐标 `(Generation, Sequence)`：代次内序号从 1 连续递增；代次翻转是显式不连续点，丢弃旧代次全部状态并请求快照。
- 只有连续应用到发布方最新 `Checkpoint` 的订阅才进入 `synced`，此时才提供有效判断；其余状态（含刚重启）一律 fail-closed 拒绝。
- 发现缺口（乱序、跳号、检查点越过本地位置）立即进入 `gap` 拒绝状态，并通过 `SnapshotRequests()` 请求完整快照。
- 快照等待期间到达的事件按序号缓冲去重；快照到达后与缓冲增量合并，一次性原子切换，调用方永远不会看到半重建的撤销集合。合并不连续则保持拒绝并再次请求快照。
- 状态经 `StateStore` 持久化（`FileStore` 原子写 + fsync）。重启后必须重新用检查点验证连续性，陈旧订阅永远不会报告已追上最新检查点。

### 用法

```go
store := revocation.NewFileStore("/var/lib/myapp/revocation-state.json")
sub, _ := revocation.NewSubscriber(store)

ctx, cancel := context.WithCancel(context.Background())
defer cancel()
subscription := revocation.Run(ctx, sub, client) // client 实现 revocation.Client
defer func() { _ = subscription.Wait() }()

if !sub.Valid(credentialID) {
    // 拒绝：已撤销，或本地视图不可信
}
```

接入方需实现 `Client`/`Stream` 接口（gRPC、WebSocket 等均可），状态机核心与传输无关，可直接驱动 `ApplyEvent`/`ApplyCheckpoint`/`ApplySnapshot`。

### 测试

```sh
go test -race ./revocation/
```

覆盖：重复事件/检查点幂等、乱序开缺口、快照期间新事件缓冲合并、快照仍不连续时保持拒绝并重试、代次翻转、重启后强制重验证、陈旧订阅不得报告已追上、断线重连后从已应用位置恢复订阅。

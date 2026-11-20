记录层同时有一条待读队列和一条待写队列，应用数据、密钥更新和关闭回调可能交错。给 Go TLS 宿主写可导入的密钥代次组件，用注入回调模拟认证与写出，不连真实网络。只有在旧读密钥下认证通过的更新才能推进读代次；对端请求回复时，回复须在正确写代次提交后才推进本端写密钥。回调可返回已提交、明确未提交或结果不明，不明时须凭消息身份查证，不能再次输出另一个更新。应用数据在入队时绑定代次，但最终交付前须核实撤销、关闭和序号窗口。两端同时请求更新不能引发回复循环，超过公开预算时不得留下半更新会话。给出可由独立事件回放核对的状态摘要；用小队列枚举两端同时更新、先取消再完成认证、写出成功后断线、关闭后的旧回调与同序号跨代的组合，核对每一帧使用的密钥代次及实际发送次数。

## keyepoch

`keyepoch` 是无网络、纯事件驱动的 TLS 1.3 KeyUpdate 状态机，可直接被宿主
`import`。所有外部效果都走注入回调，组件本身不打开任何套接字。

- `keyepoch/frame.go`：帧类型、HMAC-SHA256 代次密钥派生 `DeriveKey`、
  `SignFrame`/`VerifyFrame`，认证与撤销/序号等队列状态严格分离。
- `keyepoch/callbacks.go`：`Authenticator`、`Writer`、`CommittedLookup` 三个
  回调接口，`AuthResult`（接受/拒绝/不明）与 `WriteResult`（已提交/未提交/不明），
  以及内存 `Wire` 和确定性脚本适配器；`Wire.Commit` 按 MsgID 幂等。
- `keyepoch/engine.go`：双队列引擎。读代次只在旧读密钥下认证通过的
  KeyUpdate 后推进；回复 KeyUpdate 在正确写代次的帧真正提交后才推进写代次；
  不明写冻结 FIFO 队列并仅能经 `ResolveWrite` 按 MsgID 查证，绝不产生第二个
  更新；应用数据入队即绑定代次，交付前再过撤销、关闭、按代次独立的序号窗口；
  预算超限直接 fail-close 并清空半更新状态；`Summary.Canonical` 与 `Replay`
  提供独立事件回放核对。
- `keyepoch/enum.go`：小队列有界 DFS 枚举（默认约 4 万状态、深度 6），每个
  可达状态检查代次预算、队列代次绑定、不明写唯一且位于队首、交付去重、独立
  回放一致性等不变量，并覆盖同时更新、先取消再认证、断线、关闭后旧回调、
  同序号跨代、不明写身份查证等组合。
- `cmd/keyepoch-enum`：运行枚举并打印覆盖情况、代表性轨迹的每帧代次/序号/
  实际发送次数，以及回放核对结果。

### 运行

```sh
go test -buildvcs=false ./...
go run -buildvcs=false ./cmd/keyepoch-enum
```

宿主通过 `Config` 注入自己的回调实现，再用 `Apply(Event)` 或 `Pump`、
`Incoming`、`RequestUpdate`、`ResolveWrite` 等便捷方法驱动；`Events()` 导出
完整事件流，第三方可用 `Replay` 重新构建并逐条 diff `Canonical()` 摘要。

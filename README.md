# 有状态协议序列归约

`netsec` 把一个触发失败的协议消息序列（几十条消息、跨多条连接）归约成仍触发
同一失败的最小序列，可直接嵌入 Go 测试。

## 模型

- 每条 `Message` 带连接归属 `Conn`、生产符号 `Produces` 与消费符号 `Consumes`。
  符号用 `SymID` 表示（票据、nonce、会话 id 等），依赖可以跨连接：c2 上的消息
  可以消费 c1 上生产的票据。
- 同一符号允许有多个生产者，同一条消息也允许重复消费同一符号；归约对这两种
  “重复”都有显式处理（见 `deps.go`）。
- 删除策略二选一：
  - `Cascade`：删除一个根消息时，把失去全部生产者的消费者一起级联删除，闭包
    跨连接传播。
  - `Repair`：只删根消息，对悬空消费者调用调用方提供的 `RepairFunc` 修复；
    修复返回 false 则整个候选被拒绝。修复总是改写副本，绝不修改原消息。

## 归约保证

- **全新会话**：每个候选都通过 `Harness.NewSession()` 拿到一个全新的服务器状态，
    会话只服务一次即 `Close()`，结构上杜绝复用前次服务器状态。
- **同类失败才接受**：失败分类与超时完全由调用方的 `Classifier` 决定，只有与
    基线完全相同的失败类才接受；超时是独立的类，非单调（删过头变成另一种失败）
    的候选会被拒绝。
- **闭包审计**：到达不动点后，对当前序列里每个可删闭包再各探一次，全部不可删
    才返回；若审计中发现仍可删（如分类有波动），继续收缩并重新审计。
- 完整评估历史保存在 `Result.Runs`，审计记录保存在 `Result.ClosureChecks`。

## 用法

```go
harness := &MyHarness{} // 实现 netsec.Harness
result := netsec.MinimizeT(t, harness, trace, netsec.Options{
    Policy:   netsec.Cascade,
    Classify: classify, // func(error) (failureClass string, timedOut bool)
})
// result.Messages 即最小序列，result.TargetFailure 为基线失败类
```

## 测试

`go test ./...` 用一个内存假服务器覆盖：跨连接共享票据的级联删除、重复生产/
重复消费、非单调失败判定、超时作为不同失败类、修复策略与修复被拒、以及每个候选
使用独立全新会话（含跨会话票据复用检测）。

# 单进程事件模拟器：至少一次消息投递 + Transactional Outbox/Inbox

本示例只使用 Go 标准库，在一个进程内模拟持久存储、消息代理、网络延迟、应答丢失、进程崩溃和时间推进。模拟器不使用 goroutine，也不访问真实网络；所有行为都由虚拟时钟和确定性事件队列驱动。

## 模型

- `Store`：模拟持久 KV 存储。事务先写私有快照，`Commit()` 后才对其他事务可见；模拟崩溃后已提交数据仍在，未提交数据丢失。
- `Bus`：模拟持久消息代理。未收到处理器成功返回的投递会按固定延迟重投。
- `OrderApp`：上游订单服务。接收命令、订单业务行、接收记录（inbox）和 outbox 行在同一个本地事务中提交。
- `ShipmentApp`：下游服务。接收记录和本地运单修改在同一个本地事务中提交，用消息 ID 做幂等去重。
- `Scheduler`：单线程最小堆虚拟时钟；通信延迟、重试延迟和重启都调度为确定性事件。

代码入口：

- `sim/store.go`：事务快照、提交、回滚。
- `sim/bus.go`：至少一次投递、重试和投递指标。
- `sim/scheduler.go`：虚拟时钟与事件队列。
- `sim/fault.go`：一次性故障点。
- `sim/app.go`：上游 outbox 和下游 inbox 业务逻辑。
- `sim/scenario.go`：故障重放场景编排。
- `main.go`：运行全部示例场景。

## 故障点重放

| 故障点 | 重放窗口 | 预期结果 |
| --- | --- | --- |
| `before_order_commit` | 上游接收记录、业务行、outbox 已写入但未提交时崩溃 | 三个本地修改一起回滚；代理重投命令后重新处理；事件只发布一次。 |
| `after_order_ack` | 本地事务已提交，但命令响应/ACK 丢失 | 代理重投命令；inbox 命中，业务不再修改；提交时已安排的 outbox 仍发送。 |
| `after_outbox_send` | outbox 事件已发给代理，但 `Sent=true` 尚未提交时崩溃 | 重启扫描到未发送 outbox，再次发布同一事件；下游 inbox 去重，业务只提交一次。 |
| `before_event_commit` | 下游 inbox 和业务修改已写入但未提交时崩溃 | 两个修改一起回滚；代理重投事件后成功提交。 |

每个故障点是一次性的：它用于制造真实的非确定故障窗口；恢复后重放相同事件序列时不再故障，因此系统能演示最终收敛。虚拟时钟和事件顺序固定，所以同一配置的重放输出完全一致。

## 安全性证据

1. **未提交不外发，也没有本地部分修改。**
   上游在单个事务中写入 `inbox:<command-id>`、`order:<order-id>` 和 `outbox:evt-<command-id>`；`Bus.Publish` 只在事务提交返回成功后发生。`TestUncommittedOrderTransactionHasNoLocalOrExternalEffect` 在提交前崩溃后断言三类本地 key 和事件发布数均为零。
2. **已提交一定具备可发送的 outbox。**
   业务行和 outbox 行原子提交；提交成功后立即安排 relay，即使随后响应丢失，relay 事件仍已在虚拟队列中。进程崩溃重启时也会扫描所有 `Sent=false` 的 outbox。
3. **重发不会重复执行业务。**
   上下游都先在同一事务中检查并写入 inbox。命令重投命中上游 inbox 时直接 ACK；同一事件发布两次并两次送达时，下游只有第一次执行业务，第二次输出 `skip duplicate event`。表驱动测试还统计了业务提交日志次数，所有场景均为一次。
4. **下游接收与业务修改同生共死。**
   `inbox:<event-id>` 与 `shipment:<order-id>` 在同一事务中提交。`TestUncommittedDownstreamTransactionHasNoPartialEffect` 在提交前崩溃后断言二者都不存在，所以不会出现“已去重但业务没做”或“业务做了但无法去重”。

## 活性证据

在“故障只发生有限次、处理器在无故障时成功、未 ACK 的持久消息最终可重试”的标准假设下：

1. 命令或事件未 ACK 时，`Bus.deliver` 会把同一个持久投递重新放入虚拟时钟队列。
2. 已提交但未发送或未标记的 outbox，会在重启时由 `relayOutbox` 扫描；一次 relay 成功后把 outbox 标记为 `Sent=true`，后续扫描停止重复发布。
3. 下游 inbox 让重复投递快速成功 ACK，不会因为重复消息而永久重试。
4. 一次性故障消耗后无故障处理器返回成功，因此每条消息最终 ACK，最终状态收敛为订单已创建、outbox 已发送、下游运单已创建。

`TestAtLeastOnceScenarios` 覆盖四个故障窗口并断言最终状态；`TestReplayIsDeterministic` 比较两次完整场景字符串，证明时间、通信和故障重放均可重复。

## 运行

```sh
go run .
go test ./... -v
go vet ./...
go test -race ./...
```

本次环境中的完整输出：

- 示例输出：`docs/sample-output.txt`
- 测试输出：`docs/test-output.txt`

样例摘要：

```text
no fault:                            command attempts=1, event publishes=1, event attempts=1
crash before order commit:           command attempts=2, event publishes=1, shipment commits=1
response lost after order commit:    command attempts=2, duplicate command skipped, shipment commits=1
outbox sent before mark:             event publishes=2, duplicate event skipped, shipment commits=1
downstream crash before commit:      event attempts=2, shipment commits=1
```

测试结果：`PASS`，包含至少一次场景、未提交零副作用、下游同事务原子性、确定性重放和事务快照隔离测试。

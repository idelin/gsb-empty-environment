# Matrix 脱敏事件授权与到达顺序

Matrix 联邦里的 m.room.redaction 会清除目标事件内容，攻击者所在服务器可伪造对他人事件的脱敏，或让脱敏先于目标到达来绕过检查。给Go主服务器写供联邦 PDU 入口调用的脱敏包，以下按本题规定：脱敏事件在其发送者拥有房间 redact 权限等级，或其发送者所在服务器与目标事件发送者所在服务器相同时才授权；目标尚未到达时脱敏进入待决表，目标到达后再按脱敏事件当时的房间状态判定，上限每房间 1000 条；脱敏只保留 event_id、type、room_id、sender、state_key 以及 content 中该类型的白名单键（m.room.member 留 membership，m.room.power_levels 留 ban、kick、redact、events、users 等），其余清空。用跨服务器越权脱敏、先脱敏后到达、待决表上限、被脱敏的 power_levels 验证。

## 联邦入口

`Redactor.Ingest`（别名 `HandlePDU`）接收单个 PDU 和该 PDU 在房间 DAG 中
位置对应的 `RoomState`：

```go
redactor := netsec.NewRedactor() // 每房间最多 1000 条待决脱敏

// 脱敏先到：目标未知，进入待决表。RoomState 会在入队时深拷贝。
result, err := redactor.Ingest(redactionPDU, stateAtRedaction)

// 目标后到：按每条待决脱敏入队时的状态快照授权，并返回脱敏后的目标。
arrived, err := redactor.Ingest(targetPDU, currentState)
```

`IngestResult.Event` 是入口应持久化的事件；目标后到时它已是脱敏后的形式。
`IngestResult.Redaction` 给出脱敏的授权/应用结果，`Applied` 和 `Rejected`
给出目标到达时消费的待决条目。

调用方若已有事件存储，也可直接使用：

- `RedactNow(redaction, target, state)`：目标已知时立即判定并返回脱敏目标。
- `TargetArrived(target)`：目标到达并消费待决条目。
- `QueueRedaction(redaction, state)`：显式入队。
- `PendingCount(roomID)` / `TotalPending()`：检查待决表容量。
- `RedactEvent(target)` / `RedactPDU(map)`：纯脱敏算法。

授权只使用传入的 `RoomState.PowerLevels`（`m.room.power_levels` 事件内容）。
即使调用方在目标到达时传入新状态，待决脱敏仍使用入队时的状态快照。

## 测试

```sh
go test ./...
go test -race ./...
```

# correlator

按 **(epoch, seq)** 关联请求与响应的 Go 库（仅标准库）。处理超时、重复、
乱序与序号复用，输出延迟与孤立消息；存储有界，支持 checkpoint。

## 线格式（大端）

```
[0:2]   magic 0xC0 0xDE
[2]     kind (1=request, 2=response)
[3:11]  epoch uint64
[11:19] seq   uint64
[19:23] payload 长度 uint32
[23:]   payload
```

## 流式解析（`parser.go`）

- `Feed` 接受任意分块的字节流，`Next` 解出一帧。
- 明确区分两类失败：
  - `ErrIncomplete`：数据不够，继续喂（可恢复）；
  - `ErrInvalid`：流已损坏（magic/kind 非法、声明长度超额、缓冲超限），不可恢复。
- 声明的 payload 长度在**任何分配之前**与配额比对；缓冲区大小同样有界。

## 关联器（`correlator.go`）

- epoch 限定 seq 的作用域：**旧 epoch 的迟到响应永远不会配到新请求**。
- 仍在 pending 的重复请求记为 `EventDuplicateRequest`，**不刷新原始开始时间**。
- 超时（`Expire`）后的响应记为 `EventLate`，而不是新的成功。
- 无主的响应记为 `EventOrphan`。
- 完成或过期后 seq 可复用，作为全新请求重新计时。
- 存储有界：pending 超限拒绝（`EventRejected`），tombstone 超限按 FIFO 淘汰。

## Checkpoint（`checkpoint.go`）

`Snapshot()` 将全部状态（pending 的原始开始时间 + tombstone）序列化为有序
JSON；`Restore()` 校验并恢复，重启后延迟仍从原始开始时间起算。

## 运行

```sh
go test ./...      # 测试
go run ./cmd/demo  # 端到端演示（分块喂流、各类事件、checkpoint）
```

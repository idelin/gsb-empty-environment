# logreclaim — 复制日志回收模拟器

模拟多副本日志的回收（GC）判定：**所有活跃副本都越过水位后，水位之前的日志前缀才可回收**。

## 核心语义

- **回收判定**：水位 = 所有计入成员（活跃 + 待确认剔除）的最小复制位置，单调不降。
- **剔除两步走**：`RemoveReplica` 仅转入待确认（仍阻挡回收），`ConfirmRemoval` 后才移出水位计算。
- **代次隔离**：`Rejoin` 分配新的全局递增代次，从当前水位恢复；携带旧代次的心跳返回 `ErrStaleGeneration`，不能提升水位。
- **乱序心跳**：位置回退的心跳被忽略，副本位置与水位只增不减。

## API 一览

| 方法 | 说明 |
| --- | --- |
| `Append(n)` | 追加日志，推进日志末尾 |
| `AddReplica(id)` | 新副本加入，从当前水位恢复 |
| `Heartbeat(id, gen, pos)` | 上报复制进度（校验代次与位置） |
| `RemoveReplica(id)` / `ConfirmRemoval(id)` | 剔除 / 确认剔除 |
| `Rejoin(id)` | 重加入，分配新代次 |
| `ReclaimablePrefix()` | 可回收前缀 `[0, watermark)` |
| `BlockingNodes()` | 阻挡水位推进的节点（含待确认剔除标记） |

## 运行

```sh
go test ./...      # 测试：乱序心跳、成员变更交错、代次隔离等
go run ./cmd/simdemo   # 演示场景输出
```

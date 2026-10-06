# rulesystem

Go 规则系统：同一对象的多个事件竞争一个有效值。

## 语义

- **有效值选择**：业务优先级 `Priority` 高者胜；并列时事件序号 `Seq` 大者胜；
  再并列按 `EventID` 字典序取小者（保证确定性）。
- **撤回**：`Retract(eventID)` 只针对特定事件 id；可先于事件到达（pending
  retraction），事件后到即"到达即死"，从不产生有效值。
- **撤回赢家**：当前赢家被撤回后，下一候选自动显露并产生差量。
- **差量输出**：每次有效值变化发出一条 `Delta{ObjectID, OldPayload,
  NewPayload, HadOld, HasNew}`；非赢家变化不产生差量。
- **重复与冲突**：同一 `EventID` 完全相同地重复投递为 duplicate（忽略并计数）；
  同一 `EventID` 载荷/优先级/序号不同为 conflict（首次版本胜出，忽略并计数）。

## 实现与核对

- `rules/engine.go`：增量实现，每对象一个懒删除最大堆，O(log n) 选赢家。
- `rules/model.go`：参照模型，保留全部候选、每次全排序取头，用于核对。
- `rules/diff_test.go`：差分测试，300 个种子 × 400 条随机操作，逐条比对
  两个实现的差量流、duplicate/conflict 计数与最终有效值。

## 运行

```sh
go test ./...
```

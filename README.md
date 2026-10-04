# deletion-planner

数据删除规划器：综合对象保留期限与强引用阻挡，计算每个对象的最早可删除时间，
并将循环强引用（强连通分量）作为整体处理。弱引用不阻止删除。法律冻结等额外
限制仅按输入标记解释，对永久阻挡的对象输出原因链，对可删除对象输出删除批次。

## 语义

- **保留期限**：对象在 `retentionUntil` 之前不可删除。
- **强引用**：A 强引用 B ⇒ B 的删除时间不早于 A（引用者存活期间挡住被引用者）。
- **弱引用**：从不阻挡删除，仅在结果中回显。
- **循环强引用**：通过 Tarjan 算法求强连通分量（SCC），整组共享同一删除时间
  （组内最大保留期限与所有依赖者删除时间的最大值），并进入同一删除批次。
- **法律冻结**：`legalHold: true` 的对象永久不可删除，并沿强引用传播——凡从
  冻结对象经强引用可达的对象（含其所在环的全部成员）均被永久阻挡。每个被阻挡
  对象输出原因链：根因对象 + 强引用传播路径。
- **删除批次**：按最早可删除时间分组、按时间升序排列；无任何约束的对象进入
  `immediate` 批次。

## 输入

JSON，从文件或标准输入读取：

```json
{
  "objects": [
    {
      "id": "session",
      "retentionUntil": "2027-06-01T00:00:00Z",
      "legalHold": false,
      "strongRefs": ["user-profile"],
      "weakRefs": ["avatar-cache"]
    }
  ]
}
```

- `id`（必填，唯一）
- `retentionUntil`（可选，RFC 3339 时间；缺省表示无保留期限）
- `legalHold`（可选，布尔；`true` 表示永久冻结）
- `strongRefs` / `weakRefs`（可选，对象 id 列表）

悬空强引用为输入错误；悬空弱引用忽略并产生警告。

## 输出

```json
{
  "objects": [
    {
      "id": "user-profile",
      "permanentlyBlocked": false,
      "earliestDeletionTime": "2027-06-01T00:00:00Z",
      "batch": 2,
      "weakRefs": ["avatar-cache"]
    },
    {
      "id": "invoice",
      "permanentlyBlocked": true,
      "blockReasons": [
        { "cause": "legal-hold", "rootId": "contract-a",
          "path": ["contract-a", "contract-b", "invoice"] }
      ]
    }
  ],
  "batches": [
    { "index": 0, "immediate": true, "objectIds": ["avatar-cache"] },
    { "index": 2, "time": "2027-06-01T00:00:00Z", "objectIds": ["session", "user-profile"] }
  ],
  "warnings": []
}
```

- 可删除对象：`earliestDeletionTime`（或 `deletableImmediately: true`）+ `batch` 序号。
- 永久阻挡对象：`permanentlyBlocked: true` + `blockReasons` 原因链列表
  （每个法律冻结根因一条链），无删除时间与批次。

## 使用

```sh
go run . examples/input.json   # 从文件读取
go run . < examples/input.json # 从标准输入读取
go test ./...                  # 运行测试
```

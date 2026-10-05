# deletion-planner

数据删除规划器：根据对象保留期限、强/弱引用与法律冻结标记，计算每个对象的
最早可删除时间，输出可删除批次与永久阻挡原因链。

## 语义

- **保留期限** `retention_until`：该时间之前不可删除；缺省表示可立即删除。
- **强引用** `strong_refs`：引用方存活期间被引用方不可删除，被引用方的最早
  删除时间不早于所有强引用方。强引用环（SCC）作为整体处理，整环同一时间
  删除，取环内最大约束。
- **弱引用** `weak_refs`：仅解析校验，绝不阻止删除。
- **法律冻结等额外限制**仅按输入标记解释：
  - `legal_hold: true` 且无 `hold_until` → 永久阻挡；
  - `hold_until`（无论是否带 `legal_hold`）→ 冻结至该时间。
- **永久阻挡传播**：被永久阻挡对象强引用的对象同样永久阻挡，输出中给出从
  该对象到根因（法律冻结）的完整原因链。

## 输入

JSON，从文件参数或标准输入读取：

```json
{
  "objects": [
    {"id": "a", "retention_until": "2026-01-01T00:00:00Z"},
    {"id": "b", "strong_refs": ["a"], "weak_refs": ["c"]},
    {"id": "c", "legal_hold": true},
    {"id": "d", "legal_hold": true, "hold_until": "2030-12-31T00:00:00Z"}
  ]
}
```

重复 id、引用未知对象会报错。

## 输出

- `batches`：按最早可删除时间升序分组的批次（时间为 `0001-01-01` 表示可
  立即删除）。
- `permanently_blocked`：永久阻挡对象及其原因链（逐步的 `object` +
  `reason`）。

## 运行

```sh
go run . example.json   # 或: cat example.json | go run .
go test ./...
```

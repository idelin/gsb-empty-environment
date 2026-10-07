# snapshotscan

Go 扫描模型：**数据快照固定，权限动态校验** 的分页扫描。

## 语义

- **数据快照固定**：首次 `Scan`（空游标）时 `Pin` 当前数据版本，整个扫描
  过程只读该快照；之后的增删改对本次扫描不可见（`store.go`）。
- **权限按交付时最新版本校验**：权限不随快照固定。每个条目在交付瞬间
  调用 `PermissionModel.Allowed`，读取最新授权状态（`permission.go`）。
  - 快照后、交付前**撤权** → 条目被跳过，**不泄漏**到任何页面。
  - 快照后、交付前**授权** → 条目正常交付（动态生效）。
  - **已交付项不撤回**：交付后再撤权不影响已返回的页面。
- **游标 = 数据快照版本 + 最后扫描键**（而非最后交付键）
  （`cursor.go`）。权限过滤会跳过已扫描条目，若用"最后交付键"定位会
  错位；用"最后扫描键"保证每个快照条目恰好获得一次交付机会、按键序
  前进。跳过的条目即使之后重新授权也不会被重访。
- 快照被裁剪后继续扫描会返回 `ErrSnapshotExpired`，而不是静默读到
  错误数据。

## 核对（测试即规约）

`scanner_test.go` 用动态权限模型核对跳过项与分页：

| 测试 | 核对点 |
| --- | --- |
| `TestSnapshotPinnedAtScanStart` | 快照后增/删/改不影响扫描结果 |
| `TestRevokedBeforeDeliveryNotLeaked` | 交付前撤权的条目不出现在任何页 |
| `TestGrantedAfterSnapshotIsDelivered` | 快照后授权的条目动态生效 |
| `TestDeliveredItemsNotRetracted` | 已交付页不受后续撤权影响 |
| `TestCursorTracksLastScannedNotLastDelivered` | 游标指向最后扫描键（含被跳过项） |
| `TestSkippedItemNotRevisitedAfterRegrant` | 跳过项重新授权后也不会补发 |
| `TestPaginationAuditWithDynamicPermissions` | 页间变更权限，审计全量交付流：无泄漏、无重复、有序 |
| `TestCursorPinsSnapshotAndExpiry` | 游标固定快照版本；快照过期报错 |
| `TestPaginationCompleteAndOrdered` | 任意页大小/批大小下交付完整且有序 |

## 运行

```sh
go test ./...
```
